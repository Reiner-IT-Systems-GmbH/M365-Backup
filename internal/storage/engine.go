package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Engine holds tenant store helpers (CAS dirs, usage). Snapshots live in the catalog.
type Engine struct{}

func NewEngine() *Engine { return &Engine{} }

type SnapshotInfo struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	Source     string    `json:"source"`
	Service    string    `json:"service,omitempty"`
	Bytes      int64     `json:"bytes"`
	BytesHuman string    `json:"bytes_human,omitempty"`
	Files      int       `json:"files"`
}

// CreateRepo creates the on-disk store layout (blobs/, manifests/, exports/).
func (e *Engine) CreateRepo(ctx context.Context, repoPath, password string) error {
	_ = ctx
	_ = password
	if _, err := GuardPath(repoPath); err != nil {
		return err
	}
	for _, d := range []string{BlobsDir(repoPath), ManifestsDir(repoPath), filepath.Join(repoPath, "exports")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// InferServiceFromSource guesses exchange|onedrive|teams|sharepoint from a path.
func InferServiceFromSource(source string) string {
	s := strings.ToLower(filepath.ToSlash(source))
	switch {
	case strings.Contains(s, "/sync/exchange"), strings.HasSuffix(s, "/exchange"):
		return "exchange"
	case strings.Contains(s, "/sync/onedrive"), strings.HasSuffix(s, "/onedrive"):
		return "onedrive"
	case strings.Contains(s, "/teams"), strings.HasSuffix(s, "/teams"):
		return "teams"
	case strings.Contains(s, "/sharepoint"), strings.HasSuffix(s, "/sharepoint"):
		return "sharepoint"
	default:
		return ""
	}
}
