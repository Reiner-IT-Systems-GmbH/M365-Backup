package notification

import (
	"strings"
	"testing"
)

func TestSanitizeHeaderStripsCRLF(t *testing.T) {
	got := sanitizeHeader("ok\r\nBcc: evil@example.com")
	if strings.Contains(got, "\r") || strings.Contains(got, "\n") {
		t.Fatalf("CRLF survived: %q", got)
	}
	if !strings.Contains(got, "Bcc:") {
		t.Fatalf("expected remaining text, got %q", got)
	}
}

func TestSanitizeBodyNormalizesNewlines(t *testing.T) {
	got := sanitizeBody("a\r\nb\rc\x00d")
	want := "a\r\nb\r\ncd"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRejectSMTPMeta(t *testing.T) {
	if err := rejectSMTPMeta("ok@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := rejectSMTPMeta("bad\r\nRCPT TO:<evil@x>"); err == nil {
		t.Fatal("expected error for CRLF in address")
	}
}

func TestSafeService(t *testing.T) {
	if SafeService("OneDrive") != "onedrive" {
		t.Fatal(SafeService("OneDrive"))
	}
	if SafeService("exchange\r\nBcc:x") != "unknown" {
		t.Fatal("injection-like service must be unknown")
	}
}
