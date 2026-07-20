package crypto

import (
	"errors"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	// 32 zero bytes, base64 — test-only placeholder, not a real secret
	key := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	c, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	plain := "tenant-client-secret-placeholder"
	enc, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if enc == plain {
		t.Fatal("ciphertext equals plaintext")
	}
	dec, err := c.Decrypt(enc)
	if err != nil {
		t.Fatal(err)
	}
	if dec != plain {
		t.Fatalf("got %q want %q", dec, plain)
	}
}

func TestInvalidMasterKeyLength(t *testing.T) {
	_, err := New("dG9vc2hvcnQ=") // "tooshort"
	if !errors.Is(err, ErrInvalidMasterKey) {
		t.Fatalf("expected ErrInvalidMasterKey, got %v", err)
	}
}
