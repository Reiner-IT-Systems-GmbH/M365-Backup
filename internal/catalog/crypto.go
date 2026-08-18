package catalog

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
)

func encryptBytes(password string, plain []byte) ([]byte, error) {
	gcm, err := gcmFor(password)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func decryptBytes(password string, raw []byte) ([]byte, error) {
	gcm, err := gcmFor(password)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return nil, fmt.Errorf("invalid ciphertext")
	}
	return gcm.Open(nil, raw[:ns], raw[ns:], nil)
}

func gcmFor(password string) (cipher.AEAD, error) {
	key := sha256.Sum256([]byte(password))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
