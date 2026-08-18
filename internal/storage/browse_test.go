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

func TestListBrowseDirRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.eml"), []byte("Subject: x\r\n\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListBrowseDir(root, ".."); err == nil {
		t.Fatal("expected error for ..")
	}
	unsafeRoot := root + string(os.PathSeparator) + ".." + string(os.PathSeparator) + filepath.Base(root)
	if _, err := ListBrowseDir(unsafeRoot, ""); err == nil {
		t.Fatal("expected error for root with ..")
	}
	ents, err := ListBrowseDir(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name != "ok.eml" && ents[0].Subject != "x" {
		t.Fatalf("ents=%+v", ents)
	}
}

func TestOpenBrowseFileRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.eml"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenBrowseFile(root, "../"+filepath.Base(root)+"/ok.eml"); err == nil {
		t.Fatal("expected error for .. in rel path")
	}
	got, err := OpenBrowseFile(root, "ok.eml")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "ok.eml" {
		t.Fatalf("got %q", got)
	}
}

func TestEnrichBrowseEntryFromName(t *testing.T) {
	be := BrowseEntry{Name: "Hello_World__abc123.eml"}
	EnrichBrowseEntryFromName(be.Name, &be)
	if be.Subject != "Hello World" {
		t.Fatalf("subject=%q", be.Subject)
	}
	if be.Name != "Hello World.eml" {
		t.Fatalf("name=%q", be.Name)
	}
}
