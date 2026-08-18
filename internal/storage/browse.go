package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type BrowseEntry struct {
	Name       string    `json:"name"` // display name (e.g. subject for .eml)
	Path       string    `json:"path"` // relative path inside snapshot (real filename)
	IsDir      bool      `json:"is_dir"`
	Size       int64     `json:"size"`
	Subject    string    `json:"subject,omitempty"`
	From       string    `json:"from,omitempty"`
	To         string    `json:"to,omitempty"`
	ReceivedAt time.Time `json:"received_at,omitempty"` // from EML Date header
}

// ListBrowseDir lists one directory level under an extracted snapshot or live sync tree.
func ListBrowseDir(extractRoot, relPath string) ([]BrowseEntry, error) {
	if strings.Contains(extractRoot, "..") || strings.Contains(relPath, "..") {
		return nil, fmt.Errorf("invalid subpath")
	}
	relPath = normalizeBrowseRel(relPath)
	dir, err := safeBrowsePath(extractRoot, relPath)
	if err != nil {
		return nil, err
	}
	dir, err = GuardPath(dir)
	if err != nil {
		return nil, err
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
			if dirIsVacant(abs) {
				continue
			}
			out = append(out, BrowseEntry{Name: name, Path: p, IsDir: true})
			continue
		}
		if info.Size() == 0 {
			continue
		}
		be := BrowseEntry{
			Name:  name,
			Path:  p,
			IsDir: false,
			Size:  info.Size(),
		}
		EnrichBrowseEntryFromName(name, &be)
		EnrichEMLEntry(abs, name, &be)
		out = append(out, be)
	}
	SortBrowseEntries(out)
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
	root, err := safeBrowsePath(extractRoot, "")
	if err != nil {
		return nil, err
	}
	root, err = GuardPath(root)
	if err != nil {
		return nil, err
	}
	var out []BrowseEntry
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
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
			if dirIsVacant(path) {
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
			EnrichBrowseEntryFromName(name, &be)
			if be.Subject == "" {
				EnrichEMLEntry(path, name, &be)
			} else {
				// Filename had subject; still fill From/To cheaply only if needed for display.
				EnrichEMLEntry(path, name, &be)
			}
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
	SortBrowseEntries(out)
	return out, nil
}

// EnrichBrowsePage fills EML headers for entries under a live/extracted root.
func EnrichBrowsePage(extractRoot string, entries []BrowseEntry) {
	for i := range entries {
		e := &entries[i]
		if e.IsDir || !strings.HasSuffix(strings.ToLower(e.Path), ".eml") {
			continue
		}
		abs, err := OpenBrowseFile(extractRoot, e.Path)
		if err != nil {
			continue
		}
		EnrichEMLEntry(abs, filepath.Base(e.Path), e)
	}
}

func OpenBrowseFile(extractRoot, relPath string) (string, error) {
	if strings.Contains(extractRoot, "..") || strings.Contains(relPath, "..") {
		return "", fmt.Errorf("invalid subpath")
	}
	relPath = normalizeBrowseRel(relPath)
	if relPath == "" {
		return "", fmt.Errorf("not a file")
	}
	abs, err := safeBrowsePath(extractRoot, relPath)
	if err != nil {
		return "", err
	}
	abs, err = GuardPath(abs)
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

// dirIsVacant is a cheap empty-dir check: no immediate non-meta children.
// Avoids walking multi-GB mailbox trees (previous dirHasAnyFile).
// safeBrowsePath resolves rel under root and rejects traversal / NUL.
func safeBrowsePath(root, rel string) (string, error) {
	if root == "" || strings.Contains(root, "\x00") || strings.Contains(root, "..") {
		return "", fmt.Errorf("invalid subpath")
	}
	rel = normalizeBrowseRel(rel)
	if strings.Contains(rel, "\x00") || strings.Contains(rel, "..") {
		return "", fmt.Errorf("invalid subpath")
	}
	if rel == "" {
		rel = "."
	}
	target, err := EnsureSubpath(root, rel)
	if err != nil {
		return "", err
	}
	if strings.Contains(target, "..") {
		return "", fmt.Errorf("invalid subpath")
	}
	return target, nil
}

func dirIsVacant(root string) bool {
	root, err := GuardPath(root)
	if err != nil {
		return true
	}
	ents, err := os.ReadDir(root)
	if err != nil || len(ents) == 0 {
		return true
	}
	for _, e := range ents {
		name := e.Name()
		if name == ".extracted" || name == "BACKUP_META.txt" || name == "SNAPSHOT_ROOT.txt" {
			continue
		}
		return false
	}
	return true
}

func normalizeBrowseRel(relPath string) string {
	relPath = filepath.Clean("/" + relPath)
	relPath = strings.TrimPrefix(relPath, "/")
	if relPath == "." {
		return ""
	}
	return filepath.ToSlash(relPath)
}

// EnrichBrowseEntryFromName fills Subject/Name from "subject__shortid.eml" filenames.
func EnrichBrowseEntryFromName(fileName string, be *BrowseEntry) {
	if be.IsDir || !strings.HasSuffix(strings.ToLower(fileName), ".eml") {
		return
	}
	if subj, ok := SubjectFromEMLFilename(fileName); ok {
		be.Subject = subj
		be.Name = subj + ".eml"
		return
	}
	be.Name = fileName
}

// SortBrowseEntries puts directories first, then newest mail, then name.
func SortBrowseEntries(out []BrowseEntry) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		zi, zj := out[i].ReceivedAt.IsZero(), out[j].ReceivedAt.IsZero()
		if !zi || !zj {
			if zi != zj {
				return !zi
			}
			if !out[i].ReceivedAt.Equal(out[j].ReceivedAt) {
				return out[i].ReceivedAt.After(out[j].ReceivedAt)
			}
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
}
