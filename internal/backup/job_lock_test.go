package backup

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/rhw/m365backup/internal/crypto"
	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/storage"
	"github.com/rhw/m365backup/internal/tenant"
)

func TestEnqueueSameServiceBusy(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(db.Options{Driver: db.DriverSQLite, SQLitePath: filepath.Join(dir, "t.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	cipher, err := crypto.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	store := storage.NewEngine()
	tm := &tenant.Manager{DB: database, Cipher: cipher, KopiaRoot: filepath.Join(dir, "kopia"), Store: store}
	ten, err := tm.Create(context.Background(), tenant.CreateInput{
		Name: "T", AzureTenantID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ClientID: "cccccccc-dddd-eeee-ffff-000000000000", ClientSecret: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	r := NewRunner(database, tm, NewRegistry(), store, nil, filepath.Join(dir, "stage"), 4, slog.Default())
	j := &db.Job{TenantID: ten.ID, Service: "exchange", JobType: "delta", Status: "running"}
	if err := database.CreateJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}

	_, err = r.Enqueue(context.Background(), ten.ID, "exchange", "", "delta")
	if !errors.Is(err, ErrTenantBusy) {
		t.Fatalf("want ErrTenantBusy for second exchange job, got %v", err)
	}
}

func TestEnqueueDifferentServicesOK(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(db.Options{Driver: db.DriverSQLite, SQLitePath: filepath.Join(dir, "t.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	cipher, err := crypto.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	store := storage.NewEngine()
	tm := &tenant.Manager{DB: database, Cipher: cipher, KopiaRoot: filepath.Join(dir, "kopia"), Store: store}
	ten, err := tm.Create(context.Background(), tenant.CreateInput{
		Name: "T", AzureTenantID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ClientID: "cccccccc-dddd-eeee-ffff-000000000000", ClientSecret: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	r := NewRunner(database, tm, NewRegistry(), store, nil, filepath.Join(dir, "stage"), 4, slog.Default())
	j := &db.Job{TenantID: ten.ID, Service: "exchange", JobType: "delta", Status: "running"}
	if err := database.CreateJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}

	job, err := r.Enqueue(context.Background(), ten.ID, "onedrive", "", "delta")
	if err != nil {
		t.Fatalf("different service should enqueue, got %v", err)
	}
	if job == nil || job.Service != "onedrive" {
		t.Fatalf("unexpected job: %+v", job)
	}
}

func TestDefaultCronFor(t *testing.T) {
	expr, ok := tenant.DefaultCronFor("exchange")
	if !ok || expr != "0 * * * *" {
		t.Fatalf("got %q ok=%v", expr, ok)
	}
}

func TestCleanStagingJobRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "stage")
	outside := filepath.Join(dir, "outside.txt")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &Runner{StagingRoot: staging, Log: slog.Default()}
	r.cleanStagingJob("../outside.txt")
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("path outside staging must not be removed: %v", err)
	}
}

func TestCleanStagingJobRemovesJobDir(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "stage")
	jobID := "11111111-2222-3333-4444-555555555555"
	jobDir := filepath.Join(staging, jobID)
	if err := os.MkdirAll(jobDir, 0o700); err != nil {
		t.Fatal(err)
	}
	r := &Runner{StagingRoot: staging, Log: slog.Default()}
	r.cleanStagingJob(jobID)
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatalf("job staging dir should be removed, err=%v", err)
	}
}
