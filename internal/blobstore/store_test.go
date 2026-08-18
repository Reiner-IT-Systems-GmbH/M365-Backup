package blobstore

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPutGetDedup(t *testing.T) {
	root := t.TempDir()
	s, err := New(root, "test-store-password")
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("hello catalog blob")
	h1, err := s.Put(plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(h1) != 64 {
		t.Fatalf("hash len %d", len(h1))
	}
	h2, err := s.Put(plain)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatal("dedup hash mismatch")
	}
	got, err := s.Get(h1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("got %q", got)
	}
	// Second put must not create a sibling file.
	dir := filepath.Join(root, "blobs", h1[:2])
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Fatalf("expected 1 blob file, got %d", len(ents))
	}
}

func TestWrongPassword(t *testing.T) {
	root := t.TempDir()
	s, err := New(root, "password-a")
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.Put([]byte("secret-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := New(root, "password-b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Get(h); err == nil {
		t.Fatal("expected decrypt failure")
	}
}

func TestRejectBadHash(t *testing.T) {
	s, err := New(t.TempDir(), "pw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("../etc/passwd"); err == nil {
		t.Fatal("expected reject")
	}
	if _, err := s.Get("zz"); err == nil {
		t.Fatal("expected reject")
	}
}
