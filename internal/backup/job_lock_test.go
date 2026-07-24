package backup

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/rhw/m365backup/internal/crypto"
	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/storage"
	"github.com/rhw/m365backup/internal/tenant"
)

func TestEnqueueTenantBusy(t *testing.T) {
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

	r := NewRunner(database, tm, NewRegistry(), store, nil, filepath.Join(dir, "stage"), 2, slog.Default())
	j := &db.Job{TenantID: ten.ID, Service: "exchange", JobType: "delta", Status: "running"}
	if err := database.CreateJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}

	_, err = r.Enqueue(context.Background(), ten.ID, "onedrive", "", "delta")
	if !errors.Is(err, ErrTenantBusy) {
		t.Fatalf("want ErrTenantBusy, got %v", err)
	}
}

func TestDefaultCronFor(t *testing.T) {
	expr, ok := tenant.DefaultCronFor("exchange")
	if !ok || expr != "0 * * * *" {
		t.Fatalf("got %q ok=%v", expr, ok)
	}
}
