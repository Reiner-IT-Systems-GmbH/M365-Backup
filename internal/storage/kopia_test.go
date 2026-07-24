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

func TestSnapshotIncrementalReusesPrevious(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(filepath.Join(src, "mbox"), 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		name := filepath.Join(src, "mbox", "mail"+string(rune('a'+i))+".eml")
		if err := os.WriteFile(name, []byte("From: a\r\nSubject: s\r\n\r\nbody "+string(rune('a'+i))), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	eng := NewEngine()
	pass := "test-repo-password-placeholder"
	if err := eng.CreateRepo(t.Context(), repo, pass); err != nil {
		t.Fatal(err)
	}
	first, err := eng.Snapshot(t.Context(), repo, pass, src, "exchange")
	if err != nil {
		t.Fatal(err)
	}
	second, err := eng.Snapshot(t.Context(), repo, pass, src, "exchange")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("expected distinct snapshot IDs")
	}
	if first.Bytes != second.Bytes {
		t.Fatalf("logical size changed without edits: %d vs %d", first.Bytes, second.Bytes)
	}
	if second.Files != first.Files {
		t.Fatalf("files %d vs %d", second.Files, first.Files)
	}
}

func TestSnapshotRecreatesMissingRepoDir(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "tenant")
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "hello.txt"), []byte("hello again"), 0o600); err != nil {
		t.Fatal(err)
	}

	eng := NewEngine()
	pass := "test-repo-password-placeholder"
	if err := eng.CreateRepo(t.Context(), repo, pass); err != nil {
		t.Fatal(err)
	}
	// Simulate wiped volume / deleted Kopia storage while tenant + password remain in DB.
	if err := os.RemoveAll(RepoDataDir(repo)); err != nil {
		t.Fatal(err)
	}

	info, err := eng.Snapshot(t.Context(), repo, pass, src, "exchange")
	if err != nil {
		t.Fatalf("expected auto-recreate of missing repo: %v", err)
	}
	if info.Files != 1 {
		t.Fatalf("files=%d", info.Files)
	}
	if _, err := os.Stat(RepoDataDir(repo)); err != nil {
		t.Fatalf("repo data dir not recreated: %v", err)
	}
}
