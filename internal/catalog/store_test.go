package catalog

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rhw/m365backup/internal/crypto"
	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/storage"
	"github.com/rhw/m365backup/internal/tenant"
)

func openTest(t *testing.T) (*Store, *db.Tenant) {
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
	eng := storage.NewEngine()
	tm := &tenant.Manager{DB: database, Cipher: cipher, StoreRoot: filepath.Join(dir, "store"), Store: eng}
	ten, err := tm.Create(context.Background(), tenant.CreateInput{
		Name: "T", AzureTenantID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ClientID: "cccccccc-dddd-eeee-ffff-000000000000", ClientSecret: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, pass, err := tm.DecryptSecrets(ten)
	if err != nil {
		t.Fatal(err)
	}
	st, err := Open(database, ten.ID, ten.StorePath, pass)
	if err != nil {
		t.Fatal(err)
	}
	return st, ten
}

func TestPutUpsertDeleteBrowse(t *testing.T) {
	ctx := context.Background()
	st, _ := openTest(t)
	body := []byte("From: a@b\r\nSubject: Hello\r\n\r\nHi")
	err := st.Put(ctx, Item{
		Service: "exchange", GraphItemID: "msg-1", Mailbox: "alice@contoso.com",
		ParentPath: "Inbox", Name: "Hello__aabbccddee.eml", Subject: "Hello", FromAddr: "a@b",
	}, body)
	if err != nil {
		t.Fatal(err)
	}
	// Same graph id, new name (subject rename) upserts one row.
	err = st.Put(ctx, Item{
		Service: "exchange", GraphItemID: "msg-1", Mailbox: "alice@contoso.com",
		ParentPath: "Inbox", Name: "Hello2__aabbccddee.eml", Subject: "Hello2", FromAddr: "a@b",
	}, body)
	if err != nil {
		t.Fatal(err)
	}
	ents, err := st.BrowseLive(ctx, "exchange", "alice@contoso.com/Inbox")
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Fatalf("want 1 file, got %+v", ents)
	}
	if ents[0].Subject != "Hello2" {
		t.Fatalf("subject=%q", ents[0].Subject)
	}
	if err := st.Delete(ctx, "exchange", "msg-1"); err != nil {
		t.Fatal(err)
	}
	ents, err = st.BrowseLive(ctx, "exchange", "alice@contoso.com/Inbox")
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Fatalf("deleted item still listed: %+v", ents)
	}
	snap, err := st.CommitSnapshot(ctx, "exchange", "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Generation != 1 {
		t.Fatalf("gen=%d", snap.Generation)
	}
	// History still has the last upsert then delete — live empty, gen 1 manifest empty of live files.
	live, err := st.HasLiveItems(ctx, "exchange")
	if err != nil {
		t.Fatal(err)
	}
	if live {
		t.Fatal("expected no live items")
	}
}

func TestRematchImportID(t *testing.T) {
	ctx := context.Background()
	st, _ := openTest(t)
	short := shortID("AAMkAG-real-graph-id")
	err := st.Put(ctx, Item{
		Service: "exchange", GraphItemID: ImportEMLPrefix + short,
		Mailbox: "alice@contoso.com", ParentPath: "Inbox", Name: "Hi__" + short + ".eml", Subject: "Hi",
	}, []byte("mail-body"))
	if err != nil {
		t.Fatal(err)
	}
	err = st.Put(ctx, Item{
		Service: "exchange", GraphItemID: "AAMkAG-real-graph-id",
		Mailbox: "alice@contoso.com", ParentPath: "Inbox", Name: "Hi__" + short + ".eml", Subject: "Hi",
	}, []byte("mail-body"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.getItem(ctx, "exchange", "AAMkAG-real-graph-id")
	if err != nil {
		t.Fatal(err)
	}
	if got.Mailbox != "alice@contoso.com" {
		t.Fatalf("%+v", got)
	}
	if _, err := st.getItem(ctx, "exchange", ImportEMLPrefix+short); err == nil {
		t.Fatal("import id should be rematched away")
	}
}

func TestImportSyncAndBrowseGeneration(t *testing.T) {
	ctx := context.Background()
	st, ten := openTest(t)
	sync := filepath.Join(ten.StorePath, "sync", "exchange", "bob@contoso.com", "Inbox")
	if err := os.MkdirAll(sync, 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte("Subject: Imported\r\n\r\nHi")
	if err := os.WriteFile(filepath.Join(sync, "Imported__abcdef1234.eml"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	imported, needFull, err := st.EnsureMigrated(ctx, "exchange", "job-import")
	if err != nil {
		t.Fatal(err)
	}
	if !imported || needFull {
		t.Fatalf("imported=%v needFull=%v", imported, needFull)
	}
	if _, err := os.Stat(filepath.Join(ten.StorePath, "sync")); !os.IsNotExist(err) {
		t.Fatal("sync/ should be removed")
	}
	ents, err := st.BrowseLive(ctx, "exchange", "bob@contoso.com/Inbox")
	if err != nil || len(ents) != 1 {
		t.Fatalf("live: %v %+v", err, ents)
	}
	snaps, err := st.ListSnapshots(ctx, "exchange")
	if err != nil || len(snaps) != 1 || snaps[0].Generation != 1 {
		t.Fatalf("snaps=%+v err=%v", snaps, err)
	}
	hist, err := st.BrowseGeneration(ctx, "exchange", 1, "bob@contoso.com/Inbox")
	if err != nil || len(hist) != 1 {
		t.Fatalf("hist: %v %+v", err, hist)
	}
	_, data, err := st.OpenLiveFile(ctx, "exchange", "bob@contoso.com/Inbox/Imported__abcdef1234.eml")
	if err != nil || !bytes.Equal(data, body) {
		t.Fatalf("open: %v %q", err, data)
	}
}

func TestSmartRetentionPerService(t *testing.T) {
	ctx := context.Background()
	st, _ := openTest(t)
	for i := 0; i < 4; i++ {
		if err := st.Put(ctx, Item{
			Service: "exchange", GraphItemID: "m", Mailbox: "a@b", ParentPath: "Inbox", Name: "x.eml",
		}, []byte("mail-"+string(rune('a'+i)))); err != nil {
			t.Fatal(err)
		}
		if _, err := st.CommitSnapshot(ctx, "exchange", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Put(ctx, Item{
		Service: "onedrive", GraphItemID: "f", Mailbox: "a@b", Name: "doc.txt",
	}, []byte("file")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CommitSnapshot(ctx, "onedrive", ""); err != nil {
		t.Fatal(err)
	}
	policy := storage.RetentionPolicy{Enabled: false, KeepMin: 2, PSTKeepRuns: 1}
	deleted, err := st.ApplySmartRetention(ctx, policy)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("deleted=%d want 2", deleted)
	}
	ex, _ := st.ListSnapshots(ctx, "exchange")
	od, _ := st.ListSnapshots(ctx, "onedrive")
	if len(ex) != 2 || len(od) != 1 {
		t.Fatalf("exchange=%d onedrive=%d", len(ex), len(od))
	}
}

func TestEmptyLocalData(t *testing.T) {
	ctx := context.Background()
	st, ten := openTest(t)
	svcEmpty, tenantEmpty, err := EmptyLocalData(ctx, st.DB, ten.ID, ten.StorePath, "exchange")
	if err != nil {
		t.Fatal(err)
	}
	if !svcEmpty || !tenantEmpty {
		t.Fatalf("fresh tenant: svcEmpty=%v tenantEmpty=%v", svcEmpty, tenantEmpty)
	}

	sync := filepath.Join(ten.StorePath, "sync", "exchange", "a")
	if err := os.MkdirAll(sync, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sync, "x.eml"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	svcEmpty, tenantEmpty, err = EmptyLocalData(ctx, st.DB, ten.ID, ten.StorePath, "exchange")
	if err != nil {
		t.Fatal(err)
	}
	if svcEmpty || tenantEmpty {
		t.Fatalf("sync/ present: svcEmpty=%v tenantEmpty=%v", svcEmpty, tenantEmpty)
	}
	_ = os.RemoveAll(filepath.Join(ten.StorePath, "sync"))

	if err := st.Put(ctx, Item{
		Service: "exchange", GraphItemID: "m", Mailbox: "a@b", ParentPath: "Inbox", Name: "x.eml",
	}, []byte("mail")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CommitSnapshot(ctx, "exchange", ""); err != nil {
		t.Fatal(err)
	}
	svcEmpty, tenantEmpty, err = EmptyLocalData(ctx, st.DB, ten.ID, ten.StorePath, "exchange")
	if err != nil {
		t.Fatal(err)
	}
	if svcEmpty || tenantEmpty {
		t.Fatalf("after snapshot: svcEmpty=%v tenantEmpty=%v", svcEmpty, tenantEmpty)
	}
	odEmpty, tenantEmpty, err := EmptyLocalData(ctx, st.DB, ten.ID, ten.StorePath, "onedrive")
	if err != nil {
		t.Fatal(err)
	}
	if !odEmpty || tenantEmpty {
		t.Fatalf("onedrive still empty, tenant not: odEmpty=%v tenantEmpty=%v", odEmpty, tenantEmpty)
	}
}
