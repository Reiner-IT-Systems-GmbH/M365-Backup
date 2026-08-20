package db_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rhw/m365backup/internal/db"
)

func TestListJobLogsTailAndAfter(t *testing.T) {
	database, err := db.Open(db.Options{Driver: db.DriverSQLite, SQLitePath: filepath.Join(t.TempDir(), "logs.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	ctx := context.Background()
	ten := &db.Tenant{
		Name: "Acme", AzureTenantID: "11111111-1111-1111-1111-111111111111",
		ClientID: "cid", ClientSecret: "enc", StorePassword: "enc", StorePath: "/tmp/x", Status: "active",
	}
	if err := database.CreateTenant(ctx, ten); err != nil {
		t.Fatal(err)
	}
	j := &db.Job{TenantID: ten.ID, Service: "exchange", JobType: "delta", Status: "running"}
	if err := database.CreateJob(ctx, j); err != nil {
		t.Fatal(err)
	}

	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		if err := database.InsertJobLog(ctx, &db.JobLog{
			JobID: j.ID, Level: "info", Message: "line",
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := database.ListJobLogs(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 5 {
		t.Fatalf("expected 5 logs, got %d", len(logs))
	}

	tail, total, err := database.ListJobLogsTail(ctx, j.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(tail) != 3 {
		t.Fatalf("tail total=%d len=%d", total, len(tail))
	}
	if tail[0].CreatedAt.After(tail[1].CreatedAt) || tail[1].CreatedAt.After(tail[2].CreatedAt) {
		t.Fatal("tail not chronological")
	}

	cursor := logs[2]
	after, err := database.ListJobLogsAfter(ctx, j.ID, cursor.CreatedAt, cursor.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 {
		t.Fatalf("after cursor want 2 got %d", len(after))
	}
	if after[0].ID != logs[3].ID {
		t.Fatalf("first after=%s want %s", after[0].ID, logs[3].ID)
	}
}
