package backup

import (
	"testing"

	"github.com/rhw/m365backup/internal/catalog"
)

func TestSkipContentDownload(t *testing.T) {
	var size int64 = 100
	existing := &catalog.Item{BlobHash: "abc", Size: 100}

	if !skipContentDownload(existing, nil) {
		t.Fatal("exchange: existing blob must skip MIME download")
	}
	if !skipContentDownload(existing, &size) {
		t.Fatal("onedrive: same size must skip")
	}
	size = 99
	if skipContentDownload(existing, &size) {
		t.Fatal("onedrive: size change must re-fetch")
	}
	if skipContentDownload(nil, nil) {
		t.Fatal("missing item must fetch")
	}
	if skipContentDownload(&catalog.Item{}, nil) {
		t.Fatal("empty hash must fetch")
	}
	// Skip is catalog-only (hash present). LiveBlob no longer Stats the blob file.
	if !skipContentDownload(&catalog.Item{BlobHash: "deadbeef"}, nil) {
		t.Fatal("catalog hash must skip even without a disk Exists() check")
	}
}

func TestCatalogMetaChanged(t *testing.T) {
	it := &catalog.Item{Mailbox: "a@b", ParentPath: "Inbox", Name: "x.eml", Subject: "Hi"}
	if catalogMetaChanged(it, "a@b", "Inbox", "x.eml", "Hi") {
		t.Fatal("unchanged")
	}
	if !catalogMetaChanged(it, "a@b", "Sent", "x.eml", "Hi") {
		t.Fatal("folder move")
	}
	deleted := *it
	deleted.Deleted = true
	if !catalogMetaChanged(&deleted, "a@b", "Inbox", "x.eml", "Hi") {
		t.Fatal("undelete")
	}
}
