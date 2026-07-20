package db_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rhw/m365backup/internal/db"
)

func TestTenantAndDeltaToken(t *testing.T) {
	database, err := db.Open(db.Options{Driver: db.DriverSQLite, SQLitePath: filepath.Join(t.TempDir(), "t.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	ten := &db.Tenant{
		Name: "Acme", AzureTenantID: "11111111-1111-1111-1111-111111111111",
		ClientID: "cid", ClientSecret: "enc-placeholder", KopiaPassword: "enc-kopia",
		KopiaRepoPath: "/tmp/kopia/x", Status: "setup",
	}
	if err := database.CreateTenant(context.Background(), ten); err != nil {
		t.Fatal(err)
	}
	got, err := database.GetTenant(context.Background(), ten.ID)
	if err != nil || got.Name != "Acme" {
		t.Fatalf("get tenant: %v %+v", err, got)
	}
	if err := database.UpsertDeltaToken(context.Background(), db.DeltaToken{
		TenantID: ten.ID, Service: "exchange", UserID: "u1", Token: "tok-1",
	}); err != nil {
		t.Fatal(err)
	}
	tok, err := database.GetDeltaToken(context.Background(), ten.ID, "exchange", "u1")
	if err != nil || tok != "tok-1" {
		t.Fatalf("token=%q err=%v", tok, err)
	}
}
