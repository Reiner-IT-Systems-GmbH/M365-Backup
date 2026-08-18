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
		ClientID: "cid", ClientSecret: "enc-placeholder", StorePassword: "enc-store",
		StorePath: "/tmp/store/x", Status: "setup",
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
	if err := database.UpsertDeltaToken(context.Background(), db.DeltaToken{
		TenantID: ten.ID, Service: "onedrive", UserID: "u1", Token: "od-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteDeltaTokens(context.Background(), ten.ID, "exchange"); err != nil {
		t.Fatal(err)
	}
	if tok, err := database.GetDeltaToken(context.Background(), ten.ID, "exchange", "u1"); err == nil || tok != "" {
		t.Fatalf("exchange token should be gone, got %q err=%v", tok, err)
	}
	od, err := database.GetDeltaToken(context.Background(), ten.ID, "onedrive", "u1")
	if err != nil || od != "od-1" {
		t.Fatalf("onedrive token should remain, got %q err=%v", od, err)
	}
	if err := database.DeleteDeltaTokens(context.Background(), ten.ID, ""); err == nil {
		t.Fatal("empty service must be rejected")
	}
}

func TestUserAndAPIToken(t *testing.T) {
	database, err := db.Open(db.Options{Driver: db.DriverSQLite, SQLitePath: filepath.Join(t.TempDir(), "u.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	u, err := database.UpsertUser(ctx, "m365adminuser", "hash-1")
	if err != nil || u.Username != "m365adminuser" {
		t.Fatalf("upsert: %v %+v", err, u)
	}
	u2, err := database.UpsertUser(ctx, "m365adminuser", "hash-2")
	if err != nil || u2.ID != u.ID || u2.PasswordHash != "hash-2" {
		t.Fatalf("update: %v %+v", err, u2)
	}
	if err := database.InsertAPIToken(ctx, &db.APIToken{
		UserID: u.ID, Name: "ci", Kind: "user", TokenHash: "abc", Prefix: "m365_ab…", Scope: "read",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.InsertAPIToken(ctx, &db.APIToken{
		UserID: u.ID, Name: "legacy-env", Kind: "env", Prefix: "password", Scope: "write",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteAPITokensByKind(ctx, u.ID, "env"); err != nil {
		t.Fatal(err)
	}
	list, err := database.ListAPITokens(ctx, u.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%d err=%v", len(list), err)
	}
	got, err := database.GetAPITokenByHash(ctx, "abc")
	if err != nil || got.Name != "ci" {
		t.Fatalf("by hash: %v %+v", err, got)
	}
}

func TestOneActiveJobPerService(t *testing.T) {
	database, err := db.Open(db.Options{Driver: db.DriverSQLite, SQLitePath: filepath.Join(t.TempDir(), "j.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	ten := &db.Tenant{
		Name: "Acme", AzureTenantID: "22222222-2222-2222-2222-222222222222",
		ClientID: "cid", ClientSecret: "enc-placeholder", StorePassword: "enc-store",
		StorePath: "/tmp/store/y", Status: "active",
	}
	if err := database.CreateTenant(ctx, ten); err != nil {
		t.Fatal(err)
	}
	a := &db.Job{TenantID: ten.ID, Service: "exchange", JobType: "full", Status: "queued"}
	if err := database.CreateJob(ctx, a); err != nil {
		t.Fatal(err)
	}
	b := &db.Job{TenantID: ten.ID, Service: "exchange", JobType: "delta", Status: "queued"}
	if err := database.CreateJob(ctx, b); err == nil || !db.IsUniqueViolation(err) {
		t.Fatalf("second active exchange job must hit unique lock, got %v", err)
	}
	c := &db.Job{TenantID: ten.ID, Service: "onedrive", JobType: "full", Status: "queued"}
	if err := database.CreateJob(ctx, c); err != nil {
		t.Fatalf("other service must be allowed: %v", err)
	}
	n, err := database.CountActiveFullJobs(ctx, ten.ID)
	if err != nil || n != 2 {
		t.Fatalf("full jobs=%d err=%v", n, err)
	}
	a.Status = "success"
	if err := database.UpdateJob(ctx, a); err != nil {
		t.Fatal(err)
	}
	d := &db.Job{TenantID: ten.ID, Service: "exchange", JobType: "delta", Status: "queued"}
	if err := database.CreateJob(ctx, d); err != nil {
		t.Fatalf("after success, new exchange job must be allowed: %v", err)
	}
}

func TestListActiveAndRecentJobs(t *testing.T) {
	database, err := db.Open(db.Options{Driver: db.DriverSQLite, SQLitePath: filepath.Join(t.TempDir(), "jobs.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	ten := &db.Tenant{
		Name: "Acme", AzureTenantID: "33333333-3333-3333-3333-333333333333",
		ClientID: "cid", ClientSecret: "enc-placeholder", StorePassword: "enc-store",
		StorePath: "/tmp/store/z", Status: "active",
	}
	if err := database.CreateTenant(ctx, ten); err != nil {
		t.Fatal(err)
	}
	run := &db.Job{TenantID: ten.ID, Service: "exchange", JobType: "full", Status: "running"}
	done := &db.Job{TenantID: ten.ID, Service: "onedrive", JobType: "delta", Status: "success"}
	if err := database.CreateJob(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateJob(ctx, done); err != nil {
		t.Fatal(err)
	}
	active, err := database.ListActiveJobs(ctx)
	if err != nil || len(active) != 1 || active[0].ID != run.ID {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	recent, err := database.ListRecentJobs(ctx, 10)
	if err != nil || len(recent) != 1 || recent[0].ID != done.ID {
		t.Fatalf("recent=%+v err=%v", recent, err)
	}
}
