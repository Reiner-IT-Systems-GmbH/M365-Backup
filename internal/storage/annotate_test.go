package storage

import (
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
