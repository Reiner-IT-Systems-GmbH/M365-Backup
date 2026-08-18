package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLatestSnapshotForService(t *testing.T) {
	snaps := []SnapshotInfo{
		{ID: "new-od", Service: "onedrive"},
		{ID: "new-ex", Service: "exchange"},
		{ID: "old-ex", Service: "exchange"},
	}
	got := LatestSnapshotForService(snaps, "exchange")
	if got == nil || got.ID != "new-ex" {
		t.Fatalf("got %+v", got)
	}
	if LatestSnapshotForService(snaps, "teams") != nil {
		t.Fatal("teams")
	}
}

func TestLiveSyncRoot(t *testing.T) {
	root := t.TempDir()
	ex := filepath.Join(root, "sync", "exchange")
	if err := os.MkdirAll(ex, 0o700); err != nil {
		t.Fatal(err)
	}

	got, ok := LiveSyncRoot(root, "exchange")
	if !ok || got != ex {
		t.Fatalf("exchange: got %q ok=%v", got, ok)
	}
	if _, ok := LiveSyncRoot(root, "onedrive"); ok {
		t.Fatal("onedrive dir missing, expected false")
	}
	if _, ok := LiveSyncRoot(root, "teams"); ok {
		t.Fatal("teams is not a live-sync service")
	}
	if _, ok := LiveSyncRoot(filepath.Join(root, "..", "outside"), "exchange"); ok {
		t.Fatal("repo path with .. must be rejected")
	}
}
