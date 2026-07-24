package backup

import (
	"encoding/json"
	"testing"
)

func TestParsePSTParams(t *testing.T) {
	p, err := parsePSTParams("")
	if err != nil || p.Scope != "all" {
		t.Fatalf("empty: %+v %v", p, err)
	}
	p, err = parsePSTParams(`{"scope":"mailbox","mailbox":"a@b.com"}`)
	if err != nil || p.Scope != "mailbox" || p.Mailbox != "a@b.com" || p.Folder != "" {
		t.Fatalf("mailbox: %+v %v", p, err)
	}
	p, err = parsePSTParams(`{"scope":"folder","mailbox":"a@b.com","folder":"Inbox"}`)
	if err != nil || p.Folder != "Inbox" {
		t.Fatalf("folder: %+v %v", p, err)
	}
	if _, err := parsePSTParams(`{"scope":"mailbox"}`); err == nil {
		t.Fatal("expected error for mailbox without name")
	}
	if _, err := parsePSTParams(`{"scope":"nope"}`); err == nil {
		t.Fatal("expected error for unknown scope")
	}
}

func TestEncodePSTParams(t *testing.T) {
	s, err := EncodePSTParams("folder", "u@x.com", "Sent Items")
	if err != nil {
		t.Fatal(err)
	}
	var p PSTExportParams
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		t.Fatal(err)
	}
	if p.Scope != "folder" || p.Mailbox != "u@x.com" || p.Folder != "Sent Items" {
		t.Fatalf("%+v", p)
	}
}
