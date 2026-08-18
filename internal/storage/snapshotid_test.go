package storage

import (
	"path/filepath"
	"testing"
)

func TestValidateSnapshotID(t *testing.T) {
	ok := []string{
		"20060102T150405Z-a1b2c3d4",
		"snap-1",
		"A",
	}
	for _, id := range ok {
		if err := ValidateSnapshotID(id); err != nil {
			t.Fatalf("%q: %v", id, err)
		}
	}
	bad := []string{
		"",
		"..",
		"../etc/passwd",
		"foo/bar",
		`foo\bar`,
		"snap id",
		"../../../tmp",
	}
	for _, id := range bad {
		if err := ValidateSnapshotID(id); err == nil {
			t.Fatalf("%q: expected error", id)
		}
	}
}

func TestGuardPath(t *testing.T) {
	ok, err := GuardPath("/data/store/t/sync/exchange/Inbox/a.eml")
	if err != nil || ok == "" {
		t.Fatalf("ok path: %v", err)
	}
	for _, p := range []string{"", "/tmp/foo/../etc/passwd", "a\x00b", ".."} {
		if _, err := GuardPath(p); err == nil {
			t.Fatalf("%q: expected error", p)
		}
	}
}

func TestEnsureSubpathRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if _, err := EnsureSubpath(root, "../outside"); err == nil {
		t.Fatal("expected error")
	}
	got, err := EnsureSubpath(root, "Inbox/a.eml")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "Inbox", "a.eml") {
		t.Fatalf("got %q", got)
	}
}

func TestSnapshotFileRejectsTraversal(t *testing.T) {
	repo := t.TempDir()
	_, err := SnapshotFile(repo, "../../../etc/passwd", ".snap")
	if err == nil {
		t.Fatal("expected error")
	}
	p, err := SnapshotFile(repo, "20060102T150405Z-abcd1234", ".snap")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(repo, "snapshots", "20060102T150405Z-abcd1234.snap")
	if p != want {
		t.Fatalf("got %q want %q", p, want)
	}
}
