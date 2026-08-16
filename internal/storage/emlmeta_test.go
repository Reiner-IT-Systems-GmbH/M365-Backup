package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	body := "From: Alice <a@b.de>\r\nTo: Bob <b@c.de>\r\nDate: Mon, 15 Jan 2024 10:30:00 +0100\r\nSubject: Hallo Welt\r\n\r\nBody\r\n"
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
	if meta.ReceivedAt.IsZero() {
		t.Fatal("expected ReceivedAt from Date header")
	}
	if got := meta.ReceivedAt.UTC().Format("2006-01-02 15:04"); got != "2024-01-15 09:30" {
		t.Fatalf("ReceivedAt=%s", got)
	}
	if !EMLMatchesQuery(path, "user/Inbox/msg.eml", "msg.eml", "bob") {
		t.Fatal("expected To match")
	}
	if !EMLMatchesQuery(path, "user/Inbox/msg.eml", "msg.eml", "alice") {
		t.Fatal("expected From match")
	}
}

func TestPeekEMLMetaBOMAndExchangeStyle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Abholung_Ihrer_Edelmetall-Sendung__a1b2c3d4e5.eml")
	body := "\xEF\xBB\xBF" +
		"Received: from EUR01-HE1-obe.outbound.protection.outlook.com\r\n" +
		" by mail.example.com with HTTPS; Mon, 15 Jan 2024 10:31:00 +0100\r\n" +
		"Received: from mail.example.com (mail.example.com [192.0.2.1])\r\n" +
		"\tby mx.example.net with ESMTPS id abc\r\n" +
		"\tfor <d.kohl@koos.de>; Mon, 15 Jan 2024 10:30:50 +0100\r\n" +
		"From: =?utf-8?Q?Versand_GmbH?= <noreply@versand.example>\r\n" +
		"To: \"Kohl, D.\" <d.kohl@koos.de>\r\n" +
		"Subject: Abholung Ihrer Edelmetall-Sendung\r\n" +
		"Date: Mon, 15 Jan 2024 10:30:00 +0100\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=\"utf-8\"\r\n" +
		"\r\n" +
		"Guten Tag\r\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	be := BrowseEntry{Name: filepath.Base(path), Path: filepath.Base(path), IsDir: false, Size: int64(len(body))}
	EnrichEMLEntry(path, filepath.Base(path), &be)
	if be.From == "" || !strings.Contains(be.From, "noreply@versand.example") {
		t.Fatalf("from=%q", be.From)
	}
	if be.To == "" || !strings.Contains(be.To, "d.kohl@koos.de") {
		t.Fatalf("to=%q", be.To)
	}
	if be.ReceivedAt.IsZero() {
		t.Fatal("expected date")
	}
	if be.Subject != "Abholung Ihrer Edelmetall-Sendung" {
		t.Fatalf("subject=%q", be.Subject)
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

func TestPeekEMLMetaRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "msg.eml")
	if err := os.WriteFile(path, []byte("Subject: secret\r\n\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Keep ".." in the string — filepath.Join would clean it away.
	unsafe := path + string(os.PathSeparator) + ".." + string(os.PathSeparator) + filepath.Base(path)
	if meta := PeekEMLMeta(unsafe); meta.Subject != "" {
		t.Fatalf("traversal path leaked subject %q", meta.Subject)
	}
	if meta := PeekEMLMeta(dir); meta.Subject != "" {
		t.Fatal("directory must not be opened as EML")
	}
}

func TestEnrichKeepsFilenameSubjectWhenHeadersMissing(t *testing.T) {
	dir := t.TempDir()
	name := "Nur_Betreff__abcdef1234.eml"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("not an email"), 0o600); err != nil {
		t.Fatal(err)
	}
	be := BrowseEntry{Name: name, Path: name}
	EnrichEMLEntry(path, name, &be)
	if be.Subject != "Nur Betreff" {
		t.Fatalf("subject=%q", be.Subject)
	}
	_ = time.Time{}
}
