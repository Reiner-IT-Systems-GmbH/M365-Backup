package catalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rhw/m365backup/internal/storage"
)

// ApplySmartRetention prunes catalog_snapshots per service, then GCs unreferenced blobs.
func (s *Store) ApplySmartRetention(ctx context.Context, policy storage.RetentionPolicy) (deleted int, err error) {
	snaps, err := s.ListSnapshots(ctx, "")
	if err != nil {
		return 0, err
	}
	bySvc := map[string][]storage.SnapshotInfo{}
	meta := map[string]Snapshot{}
	for _, sn := range snaps {
		meta[sn.ID] = sn
		info := sn.Info()
		bySvc[sn.Service] = append(bySvc[sn.Service], info)
	}
	now := time.Now().UTC()
	var toDelete []Snapshot
	for _, list := range bySvc {
		sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
		keep := storage.SelectSmartKeepIDs(list, policy, now)
		for _, inf := range list {
			if keep[inf.ID] {
				continue
			}
			toDelete = append(toDelete, meta[inf.ID])
		}
	}
	for _, sn := range toDelete {
		if err := s.deleteSnapshot(ctx, sn); err != nil {
			return deleted, err
		}
		deleted++
	}
	if err := s.GCBlobs(ctx); err != nil {
		return deleted, fmt.Errorf("retention deleted %d snapshots but blob GC failed: %w", deleted, err)
	}
	return deleted, nil
}

func (s *Store) deleteSnapshot(ctx context.Context, sn Snapshot) error {
	if _, err := s.DB.SQL.ExecContext(ctx, `
		DELETE FROM catalog_changes WHERE tenant_id=? AND service=? AND generation=?`,
		s.TenantID, sn.Service, sn.Generation); err != nil {
		return err
	}
	if _, err := s.DB.SQL.ExecContext(ctx, `
		DELETE FROM catalog_snapshots WHERE tenant_id=? AND id=?`, s.TenantID, sn.ID); err != nil {
		return err
	}
	s.deleteManifest(sn.Service, sn.Generation)
	return nil
}

// GCBlobs removes encrypted blobs that no live item and no remaining generation references.
func (s *Store) GCBlobs(ctx context.Context) error {
	keep := map[string]bool{}
	rows, err := s.DB.SQL.QueryContext(ctx, `
		SELECT DISTINCT blob_hash FROM catalog_items WHERE tenant_id=? AND blob_hash!=''`, s.TenantID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			_ = rows.Close()
			return err
		}
		keep[h] = true
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	rows, err = s.DB.SQL.QueryContext(ctx, `
		SELECT DISTINCT blob_hash FROM catalog_changes WHERE tenant_id=? AND blob_hash!=''`, s.TenantID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			_ = rows.Close()
			return err
		}
		keep[h] = true
	}
	_ = rows.Close()

	hashes, err := s.Blobs.ListHashes()
	if err != nil {
		return err
	}
	for _, h := range hashes {
		if keep[h] {
			continue
		}
		_ = s.Blobs.Delete(h)
	}
	return nil
}

// Materialize writes a generation (or live) to destDir as a plaintext tree.
func (s *Store) Materialize(ctx context.Context, service, version, destDir string) error {
	destDir, err := storage.GuardPath(destDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return err
	}
	var entries []ManifestEntry
	if version == "" || version == "live" {
		items, err := s.ListLiveItems(ctx, service, "", "")
		if err != nil {
			return err
		}
		for _, it := range items {
			entries = append(entries, ManifestEntry{Path: it.RelPath(), Hash: it.BlobHash, Size: it.Size})
		}
	} else {
		sn, err := s.GetSnapshot(ctx, version)
		if err != nil {
			return err
		}
		man, err := s.ReadManifest(sn.Service, sn.Generation)
		if err != nil {
			return err
		}
		entries = man.Items
		if service == "" {
			service = sn.Service
		}
	}
	for _, e := range entries {
		if e.Path == "" || stringsContainsDotDot(e.Path) {
			continue
		}
		data, err := s.Blobs.Get(e.Hash)
		if err != nil {
			return fmt.Errorf("%s: %w", e.Path, err)
		}
		out, err := storage.EnsureSubpath(destDir, e.Path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(out, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func stringsContainsDotDot(p string) bool {
	return p == ".." || strings.Contains(p, "..")
}
