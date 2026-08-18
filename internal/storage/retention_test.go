package storage

import (
	"testing"
	"time"
)

func TestSelectSmartKeepIDs(t *testing.T) {
	now := time.Date(2026, 7, 20, 15, 0, 0, 0, time.UTC)
	snaps := []SnapshotInfo{
		{ID: "h1", CreatedAt: now.Add(-1 * time.Hour)},
		{ID: "h2", CreatedAt: now.Add(-2 * time.Hour)},
		{ID: "d1", CreatedAt: now.Add(-48 * time.Hour)},
		{ID: "d1b", CreatedAt: now.Add(-48*time.Hour - time.Hour)},
		{ID: "old", CreatedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	policy := RetentionPolicy{
		Enabled: true, KeepHours: 24, KeepDaily: 7, KeepWeekly: 4,
		KeepMonthly: 6, KeepYearly: 2, KeepMin: 2,
	}
	keep := SelectSmartKeepIDs(snaps, policy, now)
	if !keep["h1"] || !keep["h2"] {
		t.Fatalf("expected hourly window kept: %+v", keep)
	}
	if !keep["d1"] {
		t.Fatalf("expected daily keep d1: %+v", keep)
	}
	if keep["d1b"] {
		t.Fatalf("should not keep second snap same day: %+v", keep)
	}
	if keep["old"] {
		t.Fatalf("very old should be pruned: %+v", keep)
	}
}

func TestSanitizeExportName(t *testing.T) {
	got := SanitizeExportName("alice@Contoso.com")
	if got != "alice_at_Contoso.com" {
		t.Fatalf("got %q", got)
	}
}
