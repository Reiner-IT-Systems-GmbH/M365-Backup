package storage

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// snapshotIDRe matches IDs we generate (e.g. 20060102T150405Z-a1b2c3d4)
// and rejects path separators / traversal.
var snapshotIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidateSnapshotID rejects empty, oversized, or path-like snapshot IDs.
func ValidateSnapshotID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("snapshot id required")
	}
	if id == "." || id == ".." || strings.Contains(id, "..") {
		return fmt.Errorf("invalid snapshot id")
	}
	if strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid snapshot id")
	}
	if !snapshotIDRe.MatchString(id) {
		return fmt.Errorf("invalid snapshot id")
	}
	return nil
}

// SnapshotFile returns an absolute path under repoPath/snapshots for id+ext
// after validating the snapshot ID (ext like ".snap" or ".json").
func SnapshotFile(repoPath, id, ext string) (string, error) {
	if err := ValidateSnapshotID(id); err != nil {
		return "", err
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	root := filepath.Join(repoPath, "snapshots")
	return EnsureSubpath(root, id+ext)
}
