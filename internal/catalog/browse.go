package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/rhw/m365backup/internal/storage"
)

// BrowseLive lists one directory level of the current catalog (deleted=0).
func (s *Store) BrowseLive(ctx context.Context, service, rel string) ([]storage.BrowseEntry, error) {
	rel = normalizeRel(rel)
	items, err := s.ListLiveItems(ctx, service, "", "")
	if err != nil {
		return nil, err
	}
	return entriesAtPrefix(itemsToPaths(items), rel), nil
}

// SearchLive matches mailbox, path, name, subject, from.
func (s *Store) SearchLive(ctx context.Context, service, query string, limit int) ([]storage.BrowseEntry, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return s.BrowseLive(ctx, service, "")
	}
	if limit <= 0 {
		limit = 500
	}
	items, err := s.ListLiveItems(ctx, service, "", "")
	if err != nil {
		return nil, err
	}
	var out []storage.BrowseEntry
	for _, it := range items {
		be := itemEntry(it)
		if !browseMatches(be, query) {
			continue
		}
		out = append(out, be)
		if len(out) >= limit {
			break
		}
	}
	storage.SortBrowseEntries(out)
	return out, nil
}

// BrowseGeneration lists a committed generation from its on-disk manifest.
func (s *Store) BrowseGeneration(ctx context.Context, service string, generation int, rel string) ([]storage.BrowseEntry, error) {
	_ = ctx
	man, err := s.ReadManifest(service, generation)
	if err != nil {
		return nil, err
	}
	var paths []pathEntry
	for _, e := range man.Items {
		paths = append(paths, pathEntry{Path: e.Path, Size: e.Size, Hash: e.Hash})
	}
	return entriesAtPrefix(paths, normalizeRel(rel)), nil
}

func (s *Store) SearchGeneration(ctx context.Context, service string, generation int, query string, limit int) ([]storage.BrowseEntry, error) {
	_ = ctx
	query = strings.ToLower(strings.TrimSpace(query))
	if limit <= 0 {
		limit = 500
	}
	man, err := s.ReadManifest(service, generation)
	if err != nil {
		return nil, err
	}
	if query == "" {
		return s.BrowseGeneration(ctx, service, generation, "")
	}
	var out []storage.BrowseEntry
	for _, e := range man.Items {
		be := storage.BrowseEntry{Name: path.Base(e.Path), Path: e.Path, Size: e.Size}
		storage.EnrichBrowseEntryFromName(be.Name, &be)
		if !browseMatches(be, query) {
			continue
		}
		out = append(out, be)
		if len(out) >= limit {
			break
		}
	}
	storage.SortBrowseEntries(out)
	return out, nil
}

func (s *Store) OpenLiveFile(ctx context.Context, service, rel string) (name string, data []byte, err error) {
	rel = normalizeRel(rel)
	if rel == "" {
		return "", nil, fmt.Errorf("not a file")
	}
	items, err := s.ListLiveItems(ctx, service, "", "")
	if err != nil {
		return "", nil, err
	}
	for _, it := range items {
		if it.RelPath() == rel {
			data, err = s.Blobs.Get(it.BlobHash)
			return it.Name, data, err
		}
	}
	return "", nil, fmt.Errorf("not found")
}

func (s *Store) OpenGenerationFile(ctx context.Context, service string, generation int, rel string) (name string, data []byte, err error) {
	_ = ctx
	rel = normalizeRel(rel)
	man, err := s.ReadManifest(service, generation)
	if err != nil {
		return "", nil, err
	}
	for _, e := range man.Items {
		if e.Path == rel {
			data, err = s.Blobs.Get(e.Hash)
			return path.Base(e.Path), data, err
		}
	}
	return "", nil, fmt.Errorf("not found")
}

func (s *Store) ServeFile(ctx context.Context, service, version, rel string, w http.ResponseWriter) error {
	var (
		name string
		data []byte
		err  error
	)
	if version == "" || version == "live" {
		name, data, err = s.OpenLiveFile(ctx, service, rel)
	} else {
		sn, gerr := s.GetSnapshot(ctx, version)
		if gerr != nil {
			return gerr
		}
		name, data, err = s.OpenGenerationFile(ctx, sn.Service, sn.Generation, rel)
	}
	if err != nil {
		return err
	}
	disp := name
	if subj, ok := storage.SubjectFromEMLFilename(name); ok {
		disp = subj + ".eml"
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", disp))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	_, err = w.Write(data)
	return err
}

type pathEntry struct {
	Path    string
	Size    int64
	Hash    string
	Subject string
	From    string
	MTime   time.Time
}

func itemsToPaths(items []Item) []pathEntry {
	out := make([]pathEntry, 0, len(items))
	for _, it := range items {
		out = append(out, pathEntry{
			Path: it.RelPath(), Size: it.Size, Hash: it.BlobHash,
			Subject: it.Subject, From: it.FromAddr, MTime: it.MTime,
		})
	}
	return out
}

func itemEntry(it Item) storage.BrowseEntry {
	be := storage.BrowseEntry{
		Name: it.Name, Path: it.RelPath(), Size: it.Size,
		Subject: it.Subject, From: it.FromAddr, ReceivedAt: it.MTime,
	}
	if be.Subject == "" {
		storage.EnrichBrowseEntryFromName(it.Name, &be)
	} else if strings.HasSuffix(strings.ToLower(it.Name), ".eml") {
		be.Name = it.Subject + ".eml"
	}
	return be
}

func entriesAtPrefix(paths []pathEntry, rel string) []storage.BrowseEntry {
	dirSeen := map[string]bool{}
	var out []storage.BrowseEntry
	prefix := rel
	if prefix != "" {
		prefix += "/"
	}
	for _, pe := range paths {
		p := pe.Path
		if rel != "" {
			if p == rel {
				continue // the directory itself
			}
			if !strings.HasPrefix(p, prefix) {
				continue
			}
			p = strings.TrimPrefix(p, prefix)
		}
		if p == "" {
			continue
		}
		if i := strings.IndexByte(p, '/'); i >= 0 {
			dir := p[:i]
			if dirSeen[dir] {
				continue
			}
			dirSeen[dir] = true
			full := dir
			if rel != "" {
				full = rel + "/" + dir
			}
			out = append(out, storage.BrowseEntry{Name: dir, Path: full, IsDir: true})
			continue
		}
		be := storage.BrowseEntry{
			Name: path.Base(pe.Path), Path: pe.Path, Size: pe.Size,
			Subject: pe.Subject, From: pe.From, ReceivedAt: pe.MTime,
		}
		if be.Subject == "" {
			storage.EnrichBrowseEntryFromName(be.Name, &be)
		} else if strings.HasSuffix(strings.ToLower(be.Name), ".eml") {
			be.Name = be.Subject + ".eml"
		}
		out = append(out, be)
	}
	storage.SortBrowseEntries(out)
	return out
}

func browseMatches(be storage.BrowseEntry, q string) bool {
	if strings.Contains(strings.ToLower(be.Path), q) ||
		strings.Contains(strings.ToLower(be.Name), q) ||
		strings.Contains(strings.ToLower(be.Subject), q) ||
		strings.Contains(strings.ToLower(be.From), q) {
		return true
	}
	return false
}

func normalizeRel(rel string) string {
	rel = strings.Trim(strings.ReplaceAll(rel, "\\", "/"), "/")
	if rel == "." {
		return ""
	}
	return rel
}

// GetItemByPath returns a live item matching the relative path.
func (s *Store) GetItemByPath(ctx context.Context, service, rel string) (*Item, error) {
	rel = normalizeRel(rel)
	items, err := s.ListLiveItems(ctx, service, "", "")
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].RelPath() == rel {
			return &items[i], nil
		}
	}
	return nil, sql.ErrNoRows
}
