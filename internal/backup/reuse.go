package backup

import (
	"context"

	"github.com/rhw/m365backup/internal/catalog"
)

// skipContentDownload is true when Graph content must not be fetched again.
// knownSize == nil skips the size check (Exchange MIME has no size on delta).
// For OneDrive, pass Graph's file size so edits still re-download.
func skipContentDownload(existing *catalog.Item, knownSize *int64) bool {
	if existing == nil || existing.BlobHash == "" {
		return false
	}
	if knownSize != nil && existing.Size != *knownSize {
		return false
	}
	return true
}

func catalogMetaChanged(it *catalog.Item, mailbox, parent, name, subject string) bool {
	if it == nil {
		return false
	}
	return it.Deleted || it.Mailbox != mailbox || it.ParentPath != parent || it.Name != name || it.Subject != subject
}

func refreshCatalogMeta(ctx context.Context, cat *catalog.Store, existing *catalog.Item, mailbox, parent, name, subject string) error {
	if !catalogMetaChanged(existing, mailbox, parent, name, subject) {
		return nil
	}
	existing.Mailbox = mailbox
	existing.ParentPath = parent
	existing.Name = name
	existing.Subject = subject
	existing.Deleted = false
	return cat.Keep(ctx, *existing)
}
