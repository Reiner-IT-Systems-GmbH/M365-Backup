package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{
		0:        "0 B",
		500:      "500 B",
		1024:     "1.0 KB",
		1536:     "1.5 KB",
		1048576:  "1.0 MB",
		1073741824: "1.0 GB",
	}
	for n, want := range cases {
		if got := FormatBytes(n); got != want {
			t.Fatalf("%d: got %q want %q", n, got, want)
		}
	}
}

func TestMeasureUsage(t *testing.T) {
	dir := t.TempDir()
	tenant := filepath.Join(dir, "tenant")
	_ = os.MkdirAll(RepoDataDir(tenant), 0o700)
	_ = os.MkdirAll(filepath.Join(tenant, "sync", "exchange"), 0o700)
	_ = os.WriteFile(filepath.Join(tenant, "sync", "exchange", "a.eml"), []byte("hello-world-12345"), 0o600)
	_ = os.WriteFile(filepath.Join(RepoDataDir(tenant), "blob.bin"), []byte("xxxxxxxxxxxxxxxxxxxx"), 0o600)

	eng := NewEngine()
	snaps := []SnapshotInfo{{ID: "s1", Service: "exchange", Bytes: 20}}
	u, err := eng.MeasureUsage(tenant, snaps)
	if err != nil {
		t.Fatal(err)
	}
	if u.TotalBytes == 0 {
		t.Fatal("expected total > 0")
	}
	if u.SyncBytes == 0 {
		t.Fatal("expected sync > 0")
	}
	if u.SnapshotsBytes == 0 {
		t.Fatal("expected kopia repo bytes > 0")
	}
	found := false
	for _, s := range u.ByService {
		if s.Service == "exchange" && s.TotalBytes > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("exchange usage missing: %+v", u.ByService)
	}
}
