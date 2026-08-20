package catalog

import (
	"path"
	"strings"
	"time"

	"github.com/rhw/m365backup/internal/storage"
)

const (
	OpUpsert = "upsert"
	OpDelete = "delete"

	ImportEMLPrefix  = "eml:"
	ImportPathPrefix = "path:"
)

// Item is one Graph object in the live catalog.
type Item struct {
	Service     string
	GraphItemID string
	Mailbox     string
	ParentPath  string
	Name        string
	BlobHash    string
	Size        int64
	MTime       time.Time
	ContentType string
	Subject     string
	FromAddr    string
	Deleted     bool
}

func (it Item) RelPath() string {
	parts := make([]string, 0, 3)
	if it.Mailbox != "" {
		parts = append(parts, it.Mailbox)
	}
	if it.ParentPath != "" {
		parts = append(parts, strings.Split(strings.Trim(it.ParentPath, "/"), "/")...)
	}
	if it.Name != "" {
		parts = append(parts, it.Name)
	}
	return path.Join(parts...)
}

// Snapshot is one committed generation.
type Snapshot struct {
	ID         string
	TenantID   string
	Service    string
	Generation int
	JobID      string
	CreatedAt  time.Time
	ItemsLive  int
	BytesLive  int64
	// Skipped is true when CommitSnapshot reused the existing generation because
	// there were no pending catalog changes (no new manifest / ListLiveItems).
	Skipped bool
}

func (s Snapshot) Info() storage.SnapshotInfo {
	return storage.SnapshotInfo{
		ID:         s.ID,
		CreatedAt:  s.CreatedAt,
		Source:     s.Service,
		Service:    s.Service,
		Bytes:      s.BytesLive,
		BytesHuman: storage.FormatBytes(s.BytesLive),
		Files:      s.ItemsLive,
	}
}

type pendingChange struct {
	Op   string
	Item Item
}

// Manifest is the encrypted on-disk sidecar for offline restore.
type Manifest struct {
	Service    string          `json:"service"`
	Generation int             `json:"generation"`
	CreatedAt  time.Time       `json:"created_at"`
	Items      []ManifestEntry `json:"items"`
}

type ManifestEntry struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}
