package backup

import (
	"context"
	"testing"
	"time"

	"github.com/rhw/m365backup/internal/db"
)

func TestWatchdogKillsStalledJob(t *testing.T) {
	r, database, ten := newTestRunner(t)
	ctx := context.Background()

	j := &db.Job{TenantID: ten.ID, Service: "exchange", JobType: "full", Status: "running"}
	j.StartedAt = time.Now().UTC().Add(-3 * time.Hour)
	j.ProgressMessage = "stuck"
	if err := database.CreateJob(ctx, j); err != nil {
		t.Fatal(err)
	}

	w := NewWatchdog(r, database, time.Minute, 30*time.Minute)
	w.Log = r.Log
	if !w.evaluate(ctx, j, time.Now().UTC()) {
		t.Fatal("expected stall detection")
	}
	if err := r.KillStale(ctx, j.ID, "test stall"); err != nil {
		t.Fatal(err)
	}
	got, err := database.GetJob(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "error" {
		t.Fatalf("status=%s", got.Status)
	}
}

func TestWatchdogProgressResetsTimer(t *testing.T) {
	r, database, ten := newTestRunner(t)
	ctx := context.Background()
	j := &db.Job{TenantID: ten.ID, Service: "exchange", JobType: "delta", Status: "running"}
	j.StartedAt = time.Now().UTC().Add(-2 * time.Hour)
	j.BytesTransferred = 100
	if err := database.CreateJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	w := NewWatchdog(r, database, time.Minute, time.Hour)
	now := time.Now().UTC()
	if w.evaluate(ctx, j, now) {
		t.Fatal("first sight should not kill")
	}
	j.BytesTransferred = 200
	if w.evaluate(ctx, j, now.Add(90*time.Minute)) {
		t.Fatal("progress should reset stall timer")
	}
}
