package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kopia/kopia/fs"
	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/manifest"
	"github.com/kopia/kopia/snapshot"
	"github.com/kopia/kopia/snapshot/snapshotfs"
)

// ListBrowseSnapshot lists one directory level inside a Kopia snapshot without
// extracting the whole tree to disk (metadata only).
func (e *Engine) ListBrowseSnapshot(ctx context.Context, repoPath, password, snapshotID, relPath string) ([]BrowseEntry, error) {
	if err := ValidateSnapshotID(snapshotID); err != nil {
		return nil, err
	}
	if relPath != "" {
		if _, err := GuardPath(relPath); err != nil {
			return nil, err
		}
	}
	var out []BrowseEntry
	err := e.withRepo(ctx, repoPath, password, func(ctx context.Context, rep repo.Repository) error {
		root, err := snapshotRootEntry(ctx, rep, snapshotID)
		if err != nil {
			return err
		}
		ent, err := entryAtRelPath(ctx, root, relPath)
		if err != nil {
			return err
		}
		dir, ok := ent.(fs.Directory)
		if !ok {
			return fmt.Errorf("not a directory")
		}
		entries, err := fs.GetAllEntries(ctx, dir)
		if err != nil {
			return err
		}
		relNorm := normalizeBrowseRel(relPath)
		for _, child := range entries {
			name := child.Name()
			if name == ".extracted" || name == "BACKUP_META.txt" || name == "SNAPSHOT_ROOT.txt" {
				continue
			}
			p := name
			if relNorm != "" {
				p = filepath.ToSlash(filepath.Join(relNorm, name))
			}
			if _, isDir := child.(fs.Directory); isDir {
				if dirHasNoFiles(ctx, child) {
					continue
				}
				out = append(out, BrowseEntry{Name: name, Path: p, IsDir: true})
				continue
			}
			sz := child.Size()
			if sz == 0 {
				continue
			}
			be := BrowseEntry{Name: name, Path: p, IsDir: false, Size: sz}
			enrichBrowseEntryFromName(name, &be)
			if strings.HasSuffix(strings.ToLower(name), ".eml") {
				if err := enrichSnapshotEML(ctx, child, name, &be); err == nil {
					// metadata filled when readable
				}
			}
			out = append(out, be)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortBrowseEntries(out)
	return out, nil
}

// SearchBrowseSnapshot walks snapshot metadata without full extract.
// Matches path/filename and subjects embedded in EML filenames (no header I/O).
func (e *Engine) SearchBrowseSnapshot(ctx context.Context, repoPath, password, snapshotID, query string, limit int) ([]BrowseEntry, error) {
	if err := ValidateSnapshotID(snapshotID); err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return e.ListBrowseSnapshot(ctx, repoPath, password, snapshotID, "")
	}
	if limit <= 0 {
		limit = 500
	}
	var out []BrowseEntry
	err := e.withRepo(ctx, repoPath, password, func(ctx context.Context, rep repo.Repository) error {
		root, err := snapshotRootEntry(ctx, rep, snapshotID)
		if err != nil {
			return err
		}
		return walkSnapshotEntries(ctx, root, "", query, limit, &out)
	})
	if err != nil && err.Error() != "search limit reached" {
		return out, err
	}
	sortBrowseEntries(out)
	return out, nil
}

// EnrichSnapshotBrowsePage peeks EML headers for the given snapshot entries.
func (e *Engine) EnrichSnapshotBrowsePage(ctx context.Context, repoPath, password, snapshotID string, entries []BrowseEntry) error {
	if err := ValidateSnapshotID(snapshotID); err != nil {
		return err
	}
	return e.withRepo(ctx, repoPath, password, func(ctx context.Context, rep repo.Repository) error {
		root, err := snapshotRootEntry(ctx, rep, snapshotID)
		if err != nil {
			return err
		}
		for i := range entries {
			ent := &entries[i]
			if ent.IsDir || !strings.HasSuffix(strings.ToLower(ent.Path), ".eml") {
				continue
			}
			fileEnt, err := entryAtRelPath(ctx, root, ent.Path)
			if err != nil {
				continue
			}
			_ = enrichSnapshotEML(ctx, fileEnt, filepath.Base(ent.Path), ent)
		}
		return nil
	})
}

// ServeSnapshotFile streams one file from a snapshot to the HTTP response (no full extract).
func (e *Engine) ServeSnapshotFile(ctx context.Context, repoPath, password, snapshotID, relPath string, w http.ResponseWriter) error {
	if err := ValidateSnapshotID(snapshotID); err != nil {
		return err
	}
	if _, err := GuardPath(relPath); err != nil {
		return err
	}
	relPath = normalizeBrowseRel(relPath)
	if relPath == "" {
		return fmt.Errorf("not a file")
	}
	return e.withRepo(ctx, repoPath, password, func(ctx context.Context, rep repo.Repository) error {
		root, err := snapshotRootEntry(ctx, rep, snapshotID)
		if err != nil {
			return err
		}
		ent, err := entryAtRelPath(ctx, root, relPath)
		if err != nil {
			return err
		}
		if _, isDir := ent.(fs.Directory); isDir {
			return fmt.Errorf("is a directory")
		}
		name := ent.Name()
		if subj, ok := subjectFromEMLFilename(name); ok {
			name = subj + ".eml"
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
		if sz := ent.Size(); sz > 0 {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", sz))
		}
		if f, ok := ent.(fs.StreamingFile); ok {
			r, err := f.GetReader(ctx)
			if err != nil {
				return err
			}
			defer r.Close()
			_, err = io.Copy(w, r)
			return err
		}
		rf, ok := ent.(fs.File)
		if !ok {
			return fmt.Errorf("not a file")
		}
		r, err := rf.Open(ctx)
		if err != nil {
			return err
		}
		defer r.Close()
		_, err = io.Copy(w, r)
		return err
	})
}

// WriteSnapshotFile streams one file from a snapshot to w (no full extract).
func (e *Engine) WriteSnapshotFile(ctx context.Context, repoPath, password, snapshotID, relPath string, w io.Writer) (fileName string, size int64, err error) {
	if err := ValidateSnapshotID(snapshotID); err != nil {
		return "", 0, err
	}
	relPath = normalizeBrowseRel(relPath)
	if relPath == "" {
		return "", 0, fmt.Errorf("not a file")
	}
	err = e.withRepo(ctx, repoPath, password, func(ctx context.Context, rep repo.Repository) error {
		root, err := snapshotRootEntry(ctx, rep, snapshotID)
		if err != nil {
			return err
		}
		ent, err := entryAtRelPath(ctx, root, relPath)
		if err != nil {
			return err
		}
		if _, isDir := ent.(fs.Directory); isDir {
			return fmt.Errorf("is a directory")
		}
		fileName = ent.Name()
		size = ent.Size()
		if f, ok := ent.(fs.StreamingFile); ok {
			r, err := f.GetReader(ctx)
			if err != nil {
				return err
			}
			defer r.Close()
			_, err = io.Copy(w, r)
			return err
		}
		rf, ok := ent.(fs.File)
		if !ok {
			return fmt.Errorf("not a file")
		}
		r, err := rf.Open(ctx)
		if err != nil {
			return err
		}
		defer r.Close()
		_, err = io.Copy(w, r)
		return err
	})
	return fileName, size, err
}

func snapshotRootEntry(ctx context.Context, rep repo.Repository, snapshotID string) (fs.Entry, error) {
	man, err := snapshot.LoadSnapshot(ctx, rep, manifest.ID(snapshotID))
	if err != nil {
		return nil, fmt.Errorf("load snapshot: %w", err)
	}
	root, err := snapshotfs.SnapshotRoot(rep, man)
	if err != nil {
		return nil, fmt.Errorf("snapshot root: %w", err)
	}
	return root, nil
}

func normalizeBrowseRel(relPath string) string {
	relPath = filepath.Clean("/" + relPath)
	relPath = strings.TrimPrefix(relPath, "/")
	if relPath == "." {
		return ""
	}
	return filepath.ToSlash(relPath)
}

func entryAtRelPath(ctx context.Context, root fs.Entry, relPath string) (fs.Entry, error) {
	rel := normalizeBrowseRel(relPath)
	if rel == "" {
		return root, nil
	}
	cur := root
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." || strings.Contains(part, "..") {
			return nil, fmt.Errorf("invalid path")
		}
		dir, ok := cur.(fs.Directory)
		if !ok {
			return nil, fmt.Errorf("not a directory")
		}
		next, err := dir.Child(ctx, part)
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}

func dirHasNoFiles(ctx context.Context, ent fs.Entry) bool {
	d, ok := ent.(fs.Directory)
	if !ok {
		return true
	}
	if ds, ok := d.(fs.DirectoryWithSummary); ok {
		sum, err := ds.Summary(ctx)
		if err == nil && sum != nil {
			return sum.TotalFileCount == 0
		}
	}
	// No summary: keep visible rather than walking children.
	return false
}

func enrichSnapshotEML(ctx context.Context, ent fs.Entry, fileName string, be *BrowseEntry) error {
	r, err := openSnapshotFile(ctx, ent)
	if err != nil {
		return err
	}
	defer r.Close()
	EnrichEMLEntryReader(r, fileName, be)
	return nil
}

func openSnapshotFile(ctx context.Context, ent fs.Entry) (io.ReadCloser, error) {
	if f, ok := ent.(fs.StreamingFile); ok {
		return f.GetReader(ctx)
	}
	rf, ok := ent.(fs.File)
	if !ok {
		return nil, fmt.Errorf("not a file")
	}
	return rf.Open(ctx)
}

func enrichBrowseEntryFromName(fileName string, be *BrowseEntry) {
	if be.IsDir || !strings.HasSuffix(strings.ToLower(fileName), ".eml") {
		return
	}
	if subj, ok := subjectFromEMLFilename(fileName); ok {
		be.Subject = subj
		be.Name = subj + ".eml"
		return
	}
	be.Name = fileName
}

func walkSnapshotEntries(ctx context.Context, ent fs.Entry, rel, query string, limit int, out *[]BrowseEntry) error {
	name := ent.Name()
	if name == ".extracted" || name == "BACKUP_META.txt" || name == "SNAPSHOT_ROOT.txt" {
		return nil
	}

	if _, ok := ent.(fs.Directory); ok {
		if dirHasNoFiles(ctx, ent) {
			return nil
		}
		if rel != "" && (strings.Contains(strings.ToLower(rel), query) || strings.Contains(strings.ToLower(name), query)) {
			*out = append(*out, BrowseEntry{Name: name + "/", Path: rel, IsDir: true})
			if len(*out) >= limit {
				return fmt.Errorf("search limit reached")
			}
		}
		dir := ent.(fs.Directory)
		entries, err := fs.GetAllEntries(ctx, dir)
		if err != nil {
			return nil
		}
		for _, child := range entries {
			childRel := child.Name()
			if rel != "" {
				childRel = filepath.ToSlash(filepath.Join(rel, child.Name()))
			}
			if err := walkSnapshotEntries(ctx, child, childRel, query, limit, out); err != nil {
				return err
			}
		}
		return nil
	}

	if ent.Size() == 0 {
		return nil
	}
	if !emlMatchesQueryName(rel, name, query) {
		return nil
	}
	be := BrowseEntry{Name: name, Path: rel, IsDir: false, Size: ent.Size()}
	enrichBrowseEntryFromName(name, &be)
	*out = append(*out, be)
	if len(*out) >= limit {
		return fmt.Errorf("search limit reached")
	}
	return nil
}

func emlMatchesQueryName(relSlash, name, queryLower string) bool {
	if strings.Contains(strings.ToLower(relSlash), queryLower) || strings.Contains(strings.ToLower(name), queryLower) {
		return true
	}
	if subj, ok := subjectFromEMLFilename(name); ok && strings.Contains(strings.ToLower(subj), queryLower) {
		return true
	}
	return false
}

func sortBrowseEntries(out []BrowseEntry) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		// Newest mail first when Date headers are present.
		zi, zj := out[i].ReceivedAt.IsZero(), out[j].ReceivedAt.IsZero()
		if !zi || !zj {
			if zi != zj {
				return !zi // dated entries before undated
			}
			if !out[i].ReceivedAt.Equal(out[j].ReceivedAt) {
				return out[i].ReceivedAt.After(out[j].ReceivedAt)
			}
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
}
