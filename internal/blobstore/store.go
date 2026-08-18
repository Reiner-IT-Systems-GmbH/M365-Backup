package blobstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rhw/m365backup/internal/storage"
)

// Store is an AES-256-GCM content-addressed blob store.
// Hash is SHA-256 of plaintext. Path: {root}/blobs/{hash[0:2]}/{hash}.
type Store struct {
	root string
	gcm  cipher.AEAD
}

func New(root, password string) (*Store, error) {
	if _, err := storage.GuardPath(root); err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(password))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	blobs := filepath.Join(root, "blobs")
	if err := os.MkdirAll(blobs, 0o700); err != nil {
		return nil, err
	}
	return &Store{root: root, gcm: gcm}, nil
}

func Hash(plain []byte) string {
	sum := sha256.Sum256(plain)
	return hex.EncodeToString(sum[:])
}

func (s *Store) blobPath(hash string) (string, error) {
	if !validHash(hash) {
		return "", fmt.Errorf("invalid blob hash")
	}
	return storage.EnsureSubpath(s.root, filepath.Join("blobs", hash[:2], hash))
}

func validHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for _, c := range h {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Put writes plaintext if the hash is new. Returns the SHA-256 hex of plaintext.
func (s *Store) Put(plain []byte) (string, error) {
	h := Hash(plain)
	path, err := s.blobPath(h)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		return h, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	out := s.gcm.Seal(nonce, nonce, plain, nil)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return h, nil
}

func (s *Store) Get(hash string) ([]byte, error) {
	path, err := s.blobPath(hash)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ns := s.gcm.NonceSize()
	if len(raw) < ns {
		return nil, fmt.Errorf("invalid blob")
	}
	plain, err := s.gcm.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt blob: %w", err)
	}
	return plain, nil
}

func (s *Store) Exists(hash string) bool {
	path, err := s.blobPath(hash)
	if err != nil {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && st.Size() > 0
}

func (s *Store) Delete(hash string) error {
	path, err := s.blobPath(hash)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// ListHashes walks blobs/ and returns hex hashes of stored objects.
func (s *Store) ListHashes() ([]string, error) {
	root := filepath.Join(s.root, "blobs")
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".tmp") {
			return nil
		}
		if validHash(name) {
			out = append(out, name)
		}
		return nil
	})
	return out, err
}

func (s *Store) Root() string { return s.root }
