package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirIsVacant(t *testing.T) {
	root := t.TempDir()
	if !dirIsVacant(root) {
		t.Fatal("empty dir should be vacant")
	}
	meta := filepath.Join(root, "BACKUP_META.txt")
	if err := os.WriteFile(meta, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !dirIsVacant(root) {
		t.Fatal("meta-only dir should be vacant")
	}
	if err := os.WriteFile(filepath.Join(root, "mail.eml"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if dirIsVacant(root) {
		t.Fatal("dir with file should not be vacant")
	}
}

func TestEnrichBrowseEntryFromName(t *testing.T) {
	be := BrowseEntry{Name: "Hello_World__abc123.eml"}
	enrichBrowseEntryFromName(be.Name, &be)
	if be.Subject != "Hello World" {
		t.Fatalf("subject=%q", be.Subject)
	}
	if be.Name != "Hello World.eml" {
		t.Fatalf("name=%q", be.Name)
	}
}
