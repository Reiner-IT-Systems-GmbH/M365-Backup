package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDisplayNameFromEMLFilename(t *testing.T) {
	name := "Projekt_Kickoff__a1b2c3d4e5.eml"
	got := DisplayNameFor("/tmp/"+name, name)
	if got != "Projekt Kickoff.eml" {
		t.Fatalf("got %q", got)
	}
}

func TestPeekEMLMeta(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "msg.eml")
	body := "From: Alice <a@b.de>\r\nTo: Bob <b@c.de>\r\nSubject: Hallo Welt\r\n\r\nBody\r\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	meta := PeekEMLMeta(path)
	if meta.Subject != "Hallo Welt" {
		t.Fatalf("subject=%q", meta.Subject)
	}
	if meta.From != "Alice <a@b.de>" {
		t.Fatalf("from=%q", meta.From)
	}
	if meta.To != "Bob <b@c.de>" {
		t.Fatalf("to=%q", meta.To)
	}
	if !EMLMatchesQuery(path, "user/Inbox/msg.eml", "msg.eml", "bob") {
		t.Fatal("expected To match")
	}
	if !EMLMatchesQuery(path, "user/Inbox/msg.eml", "msg.eml", "alice") {
		t.Fatal("expected From match")
	}
}

func TestDecodeMIMEHeader(t *testing.T) {
	in := "=?iso-8859-1?Q?AW:_=DCberwachungsvideo_Bitte_um_Pr=FCfung?="
	got := DecodeMIMEHeader(in)
	want := "AW: Überwachungsvideo Bitte um Prüfung"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if DecodeMIMEHeader("plain subject") != "plain subject" {
		t.Fatal("plain passthrough")
	}
}

func TestPeekEMLMetaRFC2047(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "msg.eml")
	body := "Subject: =?iso-8859-1?Q?AW:_=DCberwachungsvideo_Bitte_um_Pr=FCfung?=\r\n\r\nBody\r\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	meta := PeekEMLMeta(path)
	want := "AW: Überwachungsvideo Bitte um Prüfung"
	if meta.Subject != want {
		t.Fatalf("subject=%q want %q", meta.Subject, want)
	}
}
