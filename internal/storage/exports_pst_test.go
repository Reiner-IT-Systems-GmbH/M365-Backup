package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListAndResolveExchangeMailboxFolder(t *testing.T) {
	root := t.TempDir()
	mb := filepath.Join(root, "alice@contoso.com")
	inbox := filepath.Join(mb, "Inbox")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(inbox, "a.eml"), []byte("x"), 0o600)

	mails, err := ListExchangeMailboxes(root)
	if err != nil || len(mails) != 1 || mails[0] != "alice@contoso.com" {
		t.Fatalf("mailboxes: %v %v", mails, err)
	}
	folders, err := ListExchangeFolders(root, "alice@contoso.com")
	if err != nil || len(folders) != 1 || folders[0] != "Inbox" {
		t.Fatalf("folders: %v %v", folders, err)
	}
	if _, err := ResolveExchangeMailbox(root, "../etc"); err == nil {
		t.Fatal("expected traversal reject")
	}
	if _, err := ResolveExchangeFolder(root, "alice@contoso.com", "../x"); err == nil {
		t.Fatal("expected folder traversal reject")
	}
	got, err := ResolveExchangeFolder(root, "alice@contoso.com", "Inbox")
	if err != nil || got != inbox {
		t.Fatalf("resolve folder: %q %v", got, err)
	}
}
