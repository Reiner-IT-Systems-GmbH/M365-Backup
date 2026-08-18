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
	tm := &tenant.Manager{DB: database, Cipher: cipher, StoreRoot: filepath.Join(dir, "store"), Store: store}
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

func TestEnqueueIncrementalBlockedByFull(t *testing.T) {
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
	tm := &tenant.Manager{DB: database, Cipher: cipher, StoreRoot: filepath.Join(dir, "store"), Store: store}
	ten, err := tm.Create(context.Background(), tenant.CreateInput{
		Name: "T", AzureTenantID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ClientID: "cccccccc-dddd-eeee-ffff-000000000000", ClientSecret: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	seedCatalogSnapshots(t, database, ten.ID, "exchange", "onedrive")

	r := NewRunner(database, tm, NewRegistry(), store, nil, filepath.Join(dir, "stage"), 4, slog.Default())
	j := &db.Job{TenantID: ten.ID, Service: "exchange", JobType: "full", Status: "running"}
	if err := database.CreateJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}

	_, err = r.Enqueue(context.Background(), ten.ID, "onedrive", "", "delta")
	if !errors.Is(err, ErrTenantBusy) {
		t.Fatalf("incremental must wait while a full sync is active, got %v", err)
	}
	_, err = r.Enqueue(context.Background(), ten.ID, "exchange", "", "delta")
	if !errors.Is(err, ErrTenantBusy) {
		t.Fatalf("same-service incremental must be blocked, got %v", err)
	}
}

func TestEnqueueFullOtherServiceOK(t *testing.T) {
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
	tm := &tenant.Manager{DB: database, Cipher: cipher, StoreRoot: filepath.Join(dir, "store"), Store: store}
	ten, err := tm.Create(context.Background(), tenant.CreateInput{
		Name: "T", AzureTenantID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ClientID: "cccccccc-dddd-eeee-ffff-000000000000", ClientSecret: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	seedCatalogSnapshots(t, database, ten.ID, "exchange", "onedrive")

	r := NewRunner(database, tm, NewRegistry(), store, nil, filepath.Join(dir, "stage"), 4, slog.Default())
	j := &db.Job{TenantID: ten.ID, Service: "exchange", JobType: "full", Status: "running"}
	if err := database.CreateJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}

	job, err := r.Enqueue(context.Background(), ten.ID, "onedrive", "", "full")
	if err != nil {
		t.Fatalf("second full (other service) should enqueue, got %v", err)
	}
	if job == nil || job.Service != "onedrive" || job.JobType != "full" {
		t.Fatalf("unexpected job: %+v", job)
	}
}

func TestEnqueueFullKeepsTokensWhenBusy(t *testing.T) {
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
	tm := &tenant.Manager{DB: database, Cipher: cipher, StoreRoot: filepath.Join(dir, "store"), Store: store}
	ten, err := tm.Create(context.Background(), tenant.CreateInput{
		Name: "T", AzureTenantID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ClientID: "cccccccc-dddd-eeee-ffff-000000000000", ClientSecret: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertDeltaToken(context.Background(), db.DeltaToken{
		TenantID: ten.ID, Service: "exchange", UserID: "u1", Token: "keep-me",
	}); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(database, tm, NewRegistry(), store, nil, filepath.Join(dir, "stage"), 4, slog.Default())
	j := &db.Job{TenantID: ten.ID, Service: "exchange", JobType: "delta", Status: "running"}
	if err := database.CreateJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}

	_, err = r.Enqueue(context.Background(), ten.ID, "exchange", "", "full")
	if !errors.Is(err, ErrTenantBusy) {
		t.Fatalf("want ErrTenantBusy, got %v", err)
	}
	tok, err := database.GetDeltaToken(context.Background(), ten.ID, "exchange", "u1")
	if err != nil || tok != "keep-me" {
		t.Fatalf("tokens must stay when full enqueue is refused, got %q err=%v", tok, err)
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
	tm := &tenant.Manager{DB: database, Cipher: cipher, StoreRoot: filepath.Join(dir, "store"), Store: store}
	ten, err := tm.Create(context.Background(), tenant.CreateInput{
		Name: "T", AzureTenantID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ClientID: "cccccccc-dddd-eeee-ffff-000000000000", ClientSecret: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	seedCatalogSnapshots(t, database, ten.ID, "exchange", "onedrive")

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

func TestTryRunnerLockExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".runner.lock")
	a, err := TryRunnerLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Unlock() }()
	if _, err := TryRunnerLock(path); err == nil {
		t.Fatal("second instance must not take the runner lock")
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

func TestPurgeStagingOnlyJobUUIDs(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "stage")
	jobID := "11111111-2222-3333-4444-555555555555"
	jobDir := filepath.Join(staging, jobID)
	keep := filepath.Join(staging, "store")
	if err := os.MkdirAll(jobDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(keep, 0o700); err != nil {
		t.Fatal(err)
	}
	r := &Runner{StagingRoot: staging, Log: slog.Default()}
	r.PurgeStaging()
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatalf("job dir should be purged, err=%v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("non-job dir under staging must stay: %v", err)
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

func seedCatalogSnapshots(t *testing.T, database *db.DB, tenantID string, services ...string) {
	t.Helper()
	for _, svc := range services {
		_, err := database.SQL.Exec(`INSERT INTO catalog_snapshots (id, tenant_id, service, generation, created_at, items_live, bytes_live) VALUES (?,?,?,1,CURRENT_TIMESTAMP,1,1)`,
			svc+"-snap", tenantID, svc)
		if err != nil {
			t.Fatalf("seed catalog %s: %v", svc, err)
		}
	}
}

func newTestRunner(t *testing.T) (*Runner, *db.DB, *db.Tenant) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(db.Options{Driver: db.DriverSQLite, SQLitePath: filepath.Join(dir, "t.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := crypto.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	store := storage.NewEngine()
	tm := &tenant.Manager{DB: database, Cipher: cipher, StoreRoot: filepath.Join(dir, "store"), Store: store}
	ten, err := tm.Create(context.Background(), tenant.CreateInput{
		Name: "T", AzureTenantID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ClientID: "cccccccc-dddd-eeee-ffff-000000000000", ClientSecret: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := NewRunner(database, tm, NewRegistry(), store, nil, filepath.Join(dir, "stage"), 4, slog.Default())
	return r, database, ten
}

func TestEnqueueEmptyStorePromotesDeltaToFullAndFansOut(t *testing.T) {
	r, database, ten := newTestRunner(t)
	job, err := r.Enqueue(context.Background(), ten.ID, "exchange", "", "delta")
	if err != nil {
		t.Fatal(err)
	}
	if job.JobType != "full" {
		t.Fatalf("empty store must promote delta to full, got %s", job.JobType)
	}
	jobs, err := database.ListJobs(context.Background(), ten.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, j := range jobs {
		got[j.Service] = j.JobType
	}
	for _, svc := range []string{"exchange", "onedrive", "teams", "sharepoint"} {
		if got[svc] != "full" {
			t.Fatalf("want full %s, got %q (jobs=%v)", svc, got[svc], got)
		}
	}
	if _, ok := got["pst"]; ok {
		t.Fatalf("pst must not be fan-out: %v", got)
	}
}

func TestEnqueueWithSyncTreeStaysDelta(t *testing.T) {
	r, database, ten := newTestRunner(t)
	sync := filepath.Join(ten.StorePath, "sync", "exchange", "bob@contoso.com")
	if err := os.MkdirAll(sync, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sync, "Imported__abcdef1234.eml"), []byte("mail"), 0o600); err != nil {
		t.Fatal(err)
	}
	job, err := r.Enqueue(context.Background(), ten.ID, "exchange", "", "delta")
	if err != nil {
		t.Fatal(err)
	}
	if job.JobType != "delta" {
		t.Fatalf("sync/ import path must keep delta, got %s", job.JobType)
	}
	jobs, err := database.ListJobs(context.Background(), ten.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("no tenant-wide fan-out when sync/ exists, got %d jobs", len(jobs))
	}
}

func TestEnqueueEmptyStoreFullAlsoFansOut(t *testing.T) {
	r, database, ten := newTestRunner(t)
	job, err := r.Enqueue(context.Background(), ten.ID, "exchange", "", "full")
	if err != nil {
		t.Fatal(err)
	}
	if job.JobType != "full" {
		t.Fatalf("got %s", job.JobType)
	}
	jobs, err := database.ListJobs(context.Background(), ten.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) < 4 {
		t.Fatalf("consent/full on empty store must fan-out enabled services, got %d", len(jobs))
	}
}

func TestEnqueueMissingServiceBecomesFull(t *testing.T) {
	r, database, ten := newTestRunner(t)
	seedCatalogSnapshots(t, database, ten.ID, "exchange")
	job, err := r.Enqueue(context.Background(), ten.ID, "onedrive", "", "delta")
	if err != nil {
		t.Fatal(err)
	}
	if job.JobType != "full" {
		t.Fatalf("onedrive with no local data must be full, got %s", job.JobType)
	}
	jobs, err := database.ListJobs(context.Background(), ten.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("tenant not empty — no fan-out, got %d jobs", len(jobs))
	}
}
