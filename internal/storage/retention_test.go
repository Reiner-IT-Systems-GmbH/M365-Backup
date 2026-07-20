package storage

import (
	"fmt"
	"os"
	"path/filepath"
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

func TestSmartRetentionWithKopia(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "tenant")
	eng := NewEngine()
	pass := "test-repo-password-placeholder"
	ctx := t.Context()
	if err := eng.CreateRepo(ctx, repoPath, pass); err != nil {
		t.Fatal(err)
	}

	// Four exchange snapshots with unique content (avoid identical roots).
	for i := 0; i < 4; i++ {
		src := filepath.Join(dir, fmt.Sprintf("src-%d", i))
		if err := os.MkdirAll(src, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "mail.eml"), []byte(fmt.Sprintf("message-%d", i)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := eng.Snapshot(ctx, repoPath, pass, src, "exchange"); err != nil {
			t.Fatal(err)
		}
		// Distinct timestamps for Stable ordering / Smart Recycle slots.
		time.Sleep(20 * time.Millisecond)
	}

	// One onedrive snap must not be pruned by exchange KeepMin.
	od := filepath.Join(dir, "od")
	_ = os.MkdirAll(od, 0o700)
	_ = os.WriteFile(filepath.Join(od, "file.bin"), []byte("onedrive-data"), 0o600)
	if _, err := eng.Snapshot(ctx, repoPath, pass, od, "onedrive"); err != nil {
		t.Fatal(err)
	}

	policy := RetentionPolicy{
		Enabled: false, // count-based KeepMin only
		KeepMin: 2,
	}
	deleted, err := eng.ApplySmartRetention(ctx, repoPath, pass, policy)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("deleted=%d want 2 (4 exchange → keep 2)", deleted)
	}

	snaps, err := eng.ListSnapshots(ctx, repoPath, pass)
	if err != nil {
		t.Fatal(err)
	}
	var exchange, onedrive int
	for _, sn := range snaps {
		switch sn.Service {
		case "exchange":
			exchange++
		case "onedrive":
			onedrive++
		}
	}
	if exchange != 2 {
		t.Fatalf("exchange snaps=%d want 2; all=%+v", exchange, snaps)
	}
	if onedrive != 1 {
		t.Fatalf("onedrive snaps=%d want 1 (per-service Smart Recycle)", onedrive)
	}
}

