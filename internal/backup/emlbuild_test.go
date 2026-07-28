package backup

import (
	"encoding/base64"
	"io"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestBuildEMLSimpleText(t *testing.T) {
	raw, err := buildEML(emlMessage{
		From:      formatMailbox("Alice", "alice@example.com"),
		To:        []string{formatMailbox("", "bob@example.com")},
		Subject:   "Hello",
		Date:      time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
		MessageID: "<msg-1@example.com>",
		BodyType:  "text",
		Body:      "Hi Bob\r\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse eml: %v\n%s", err, raw)
	}
	if got := msg.Header.Get("X-M365Backup-Source"); got != "graph-json-fallback" {
		t.Fatalf("source header: %q", got)
	}
	if got := msg.Header.Get("Subject"); got != "Hello" {
		t.Fatalf("subject: %q", got)
	}
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(body), "\r\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	if string(dec) != "Hi Bob\r\n" {
		t.Fatalf("body: %q", dec)
	}
}

func TestBuildEMLWithAttachmentAndUnicodeSubject(t *testing.T) {
	raw, err := buildEML(emlMessage{
		From:     "a@example.com",
		To:       []string{"b@example.com"},
		Subject:  "Überprüfung",
		BodyType: "html",
		Body:     "<p>ok</p>",
		Attachments: []emlPart{{
			ContentType: "application/pdf",
			Disposition: "attachment",
			FileName:    "bericht.pdf",
			Data:        []byte("%PDF-1.4"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "multipart/mixed") {
		t.Fatalf("expected multipart:\n%s", s)
	}
	if !strings.Contains(s, "filename=bericht.pdf") && !strings.Contains(s, `filename="bericht.pdf"`) {
		t.Fatalf("missing filename:\n%s", s)
	}
	if !strings.Contains(s, "X-M365Backup-Source: graph-json-fallback") {
		t.Fatal("missing source marker")
	}
	if strings.Contains(s, "Subject: Überprüfung\r\n") {
		t.Fatal("subject should be MIME-word encoded")
	}
	if !strings.Contains(strings.ToLower(s), "subject: =?utf-8?") {
		t.Fatalf("expected encoded subject:\n%s", s)
	}
}

func TestIsMimeConversionFailed(t *testing.T) {
	err := errString(`GET …/$value: status 500: {"error":{"code":"ErrorMimeContentConversionFailed","message":"MIME content conversion failed."}}`)
	if !isMimeConversionFailed(err) {
		t.Fatal("expected conversion failure detection")
	}
	if isMimeConversionFailed(errString("status 404")) {
		t.Fatal("404 must not match")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
