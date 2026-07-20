package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type BrowseEntry struct {
	Name    string `json:"name"`  // display name (e.g. subject for .eml)
	Path    string `json:"path"`  // relative path inside snapshot (real filename)
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	Subject string `json:"subject,omitempty"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
}

// EnsureExtracted decrypts the snapshot into cacheDir/<snapshotID>/ once and returns that path.
func (e *Engine) EnsureExtracted(ctx context.Context, repoPath, password, snapshotID, cacheRoot string) (string, error) {
	if err := ValidateSnapshotID(snapshotID); err != nil {
		return "", err
	}
	browseRoot := filepath.Join(cacheRoot, "browse")
	if err := os.MkdirAll(browseRoot, 0o700); err != nil {
		return "", err
	}
	dest, err := EnsureSubpath(browseRoot, snapshotID)
	if err != nil {
		return "", err
	}
	marker := filepath.Join(dest, ".extracted")
	if _, err := os.Stat(marker); err == nil {
		return dest, nil
	}
	_ = os.RemoveAll(dest)
	if err := e.Restore(ctx, repoPath, password, snapshotID, dest); err != nil {
		_ = os.RemoveAll(dest)
		return "", err
	}
	if err := os.WriteFile(marker, []byte("ok"), 0o600); err != nil {
		return "", err
	}
	return dest, nil
}

// ListBrowseDir lists one directory level under an extracted snapshot (relPath empty = root).
func ListBrowseDir(extractRoot, relPath string) ([]BrowseEntry, error) {
	relPath = filepath.Clean("/" + relPath)
	relPath = strings.TrimPrefix(relPath, "/")
	dir := extractRoot
	if relPath != "" && relPath != "." {
		var err error
		dir, err = EnsureSubpath(extractRoot, relPath)
		if err != nil {
			return nil, err
		}
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []BrowseEntry
	for _, de := range ents {
		name := de.Name()
		if name == ".extracted" || name == "BACKUP_META.txt" || name == "SNAPSHOT_ROOT.txt" {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		p := name
		if relPath != "" && relPath != "." {
			p = filepath.ToSlash(filepath.Join(relPath, name))
		}
		abs := filepath.Join(dir, name)
		if de.IsDir() {
			if !dirHasAnyFile(abs) {
				continue // hide empty folders (and trees with only empty dirs)
			}
		}
		be := BrowseEntry{
			Name:  name,
			Path:  p,
			IsDir: de.IsDir(),
			Size:  info.Size(),
		}
		if !be.IsDir {
			if info.Size() == 0 {
				continue
			}
			EnrichEMLEntry(abs, name, &be)
			if be.Name == name || be.Name == "" {
				be.Name = DisplayNameFor(abs, name)
			}
		}
		out = append(out, be)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// SearchBrowse walks the tree and matches path, filename, or EML Subject/From.
func SearchBrowse(extractRoot, query string, limit int) ([]BrowseEntry, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return ListBrowseDir(extractRoot, "")
	}
	if limit <= 0 {
		limit = 500
	}
	var out []BrowseEntry
	err := filepath.Walk(extractRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		name := info.Name()
		if name == ".extracted" || name == "BACKUP_META.txt" || name == "SNAPSHOT_ROOT.txt" {
			return nil
		}
		rel, err := filepath.Rel(extractRoot, path)
		if err != nil || rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if info.IsDir() {
			if !dirHasAnyFile(path) {
				return filepath.SkipDir
			}
			if strings.Contains(strings.ToLower(relSlash), query) || strings.Contains(strings.ToLower(name), query) {
				out = append(out, BrowseEntry{Name: name + "/", Path: relSlash, IsDir: true, Size: 0})
			}
		} else {
			if info.Size() == 0 {
				return nil
			}
			if !EMLMatchesQuery(path, relSlash, name, query) {
				return nil
			}
			be := BrowseEntry{
				Name:  name,
				Path:  relSlash,
				IsDir: false,
				Size:  info.Size(),
			}
			EnrichEMLEntry(path, name, &be)
			if be.Name == name || be.Name == "" {
				be.Name = DisplayNameFor(path, name)
			}
			out = append(out, be)
		}
		if len(out) >= limit {
			return fmt.Errorf("search limit reached")
		}
		return nil
	})
	if err != nil && err.Error() != "search limit reached" {
		return out, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// OpenBrowseFile returns an absolute path to a file inside an extracted snapshot.
func OpenBrowseFile(extractRoot, relPath string) (string, error) {
	relPath = filepath.Clean("/" + relPath)
	relPath = strings.TrimPrefix(relPath, "/")
	if relPath == "" || relPath == "." {
		return "", fmt.Errorf("not a file")
	}
	abs, err := EnsureSubpath(extractRoot, relPath)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", fmt.Errorf("is a directory")
	}
	return abs, nil
}

// dirHasAnyFile reports whether root contains at least one non-empty regular file.
func dirHasAnyFile(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return err
		}
		name := d.Name()
		if name == ".extracted" || name == "BACKUP_META.txt" || name == "SNAPSHOT_ROOT.txt" {
			if d.IsDir() {
				return nil
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() == 0 {
			return nil
		}
		found = true
		return filepath.SkipAll
	})
	return found
}
