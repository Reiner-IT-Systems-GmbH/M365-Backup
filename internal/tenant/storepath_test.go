package tenant

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rhw/m365backup/internal/crypto"
	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/storage"
)

func TestBindStorePathRebasesOntoStoreRoot(t *testing.T) {
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
	root := filepath.Join(dir, "store")
	m := &Manager{DB: database, Cipher: cipher, StoreRoot: root, Store: storage.NewEngine()}
	ten, err := m.Create(context.Background(), CreateInput{
		Name: "T", AzureTenantID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ClientID: "cccccccc-dddd-eeee-ffff-000000000000", ClientSecret: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "old-kopia-path", ten.ID)
	ten.StorePath = stale
	if err := m.DB.UpdateTenant(context.Background(), ten); err != nil {
		t.Fatal(err)
	}
	old, err := m.BindStorePath(context.Background(), ten)
	if err != nil {
		t.Fatal(err)
	}
	if old != stale {
		t.Fatalf("previous=%s", old)
	}
	want := filepath.Join(root, ten.ID)
	if ten.StorePath != want {
		t.Fatalf("got %s want %s", ten.StorePath, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatal(err)
	}
	got, err := database.GetTenant(context.Background(), ten.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.StorePath != want {
		t.Fatalf("db path %s", got.StorePath)
	}
}
