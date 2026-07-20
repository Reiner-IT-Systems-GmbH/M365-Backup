package backup

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeRejectsTraversal(t *testing.T) {
	cases := map[string]string{
		"..":      "_",
		".":       "_",
		"":        "_",
		"a/b":     "a_b",
		"normal":  "normal",
		"foo..bar": "foo_bar",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Fatalf("sanitize(%q)=%q want %q", in, got, want)
		}
	}
}

func TestUnderRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sync", "user")
	inside := filepath.Join(root, "folder", "file.txt")
	outside := filepath.Join(root, "..", "other")
	if !underRoot(root, inside) {
		t.Fatal("inside should be under root")
	}
	if underRoot(root, outside) {
		t.Fatal("escaped path must not be under root")
	}
}

func TestEMLFileNameDecodesMIME(t *testing.T) {
	in := "=?iso-8859-1?Q?AW:_=DCberwachungsvideo_Bitte_um_Pr=FCfung?="
	got := emlFileName(in, "AAMkAGI2")
	if !strings.HasPrefix(got, "AW_ Überwachungsvideo Bitte um Prüfung__") || !strings.HasSuffix(got, ".eml") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "=?") || strings.Contains(got, "?Q?") {
		t.Fatalf("still MIME-encoded: %q", got)
	}
}
