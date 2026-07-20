package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "hello.txt"), []byte("hello m365"), 0o600); err != nil {
		t.Fatal(err)
	}

	eng := NewEngine()
	pass := "test-repo-password-placeholder"
	if err := eng.CreateRepo(t.Context(), repo, pass); err != nil {
		t.Fatal(err)
	}
	info, err := eng.Snapshot(t.Context(), repo, pass, src, "exchange")
	if err != nil {
		t.Fatal(err)
	}
	if info.Files != 1 {
		t.Fatalf("files=%d", info.Files)
	}
	if err := eng.Restore(t.Context(), repo, pass, info.ID, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello m365" {
		t.Fatalf("got %q", got)
	}
}
