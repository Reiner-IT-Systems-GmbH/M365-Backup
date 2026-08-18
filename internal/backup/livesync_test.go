package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLiveSyncService(t *testing.T) {
	if liveSyncService("exchange") != "exchange" || liveSyncService("pst") != "exchange" {
		t.Fatal("exchange/pst")
	}
	if liveSyncService("teams") != "" {
		t.Fatal("teams has no persistent live-sync")
	}
}

func TestDirHasData(t *testing.T) {
	empty := t.TempDir()
	if dirHasData(empty) {
		t.Fatal("empty dir")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	if dirHasData(root) {
		t.Fatal("only dirs")
	}
	if err := os.WriteFile(filepath.Join(root, "a", "b", "x.eml"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !dirHasData(root) {
		t.Fatal("file should count")
	}
}

func TestDiscardLiveSync(t *testing.T) {
	repo := t.TempDir()
	ex := filepath.Join(repo, "sync", "exchange")
	if err := os.MkdirAll(ex, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ex, "m.eml"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &Runner{}
	r.discardLiveSync(repo, "exchange")
	if _, err := os.Stat(ex); !os.IsNotExist(err) {
		t.Fatalf("expected removed, err=%v", err)
	}

	if err := os.MkdirAll(ex, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ex, "m.eml"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.KeepLiveSync = true
	r.discardLiveSync(repo, "exchange")
	if _, err := os.Stat(ex); err != nil {
		t.Fatal("KeepLiveSync must leave the tree")
	}
}
