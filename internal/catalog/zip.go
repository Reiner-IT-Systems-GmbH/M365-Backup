package catalog

import (
	"archive/zip"
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/rhw/m365backup/internal/storage"
)

// ZipItems writes a ZIP of catalog items (mailbox/folder scoped) to destZip.
func (s *Store) ZipItems(ctx context.Context, service, mailbox, folder, destZip string) (files int, nbytes int64, err error) {
	items, err := s.ListLiveItems(ctx, service, mailbox, folder)
	if err != nil {
		return 0, 0, err
	}
	destZip, err = storage.GuardPath(destZip)
	if err != nil {
		return 0, 0, err
	}
	if err := os.MkdirAll(filepath.Dir(destZip), 0o700); err != nil {
		return 0, 0, err
	}
	f, err := os.Create(destZip)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()
	for _, it := range items {
		if it.BlobHash == "" {
			continue
		}
		data, err := s.Blobs.Get(it.BlobHash)
		if err != nil {
			return files, nbytes, err
		}
		rel := it.RelPath()
		if mailbox != "" {
			rel = strings.TrimPrefix(rel, mailbox+"/")
		}
		w, err := zw.Create(path.Clean(rel))
		if err != nil {
			return files, nbytes, err
		}
		n, err := w.Write(data)
		if err != nil {
			return files, nbytes, err
		}
		files++
		nbytes += int64(n)
	}
	return files, nbytes, nil
}

// ExportZip materializes a snapshot generation into a zip file under workDir.
func (s *Store) ExportZip(ctx context.Context, snapshotID, workDir string) (zipPath string, err error) {
	if err := storage.ValidateSnapshotID(snapshotID); err != nil {
		return "", err
	}
	dest, err := storage.EnsureSubpath(workDir, "restore-"+snapshotID)
	if err != nil {
		return "", err
	}
	sn, err := s.GetSnapshot(ctx, snapshotID)
	if err != nil {
		return "", err
	}
	if err := s.Materialize(ctx, sn.Service, snapshotID, dest); err != nil {
		return "", err
	}
	zipPath, err = storage.EnsureSubpath(workDir, snapshotID+".zip")
	if err != nil {
		return "", err
	}
	if _, _, err := storage.ZipDirCounted(dest, zipPath); err != nil {
		return "", err
	}
	return zipPath, nil
}

func (s *Store) SnapshotInfos(ctx context.Context, service string) ([]storage.SnapshotInfo, error) {
	snaps, err := s.ListSnapshots(ctx, service)
	if err != nil {
		return nil, err
	}
	out := make([]storage.SnapshotInfo, 0, len(snaps))
	for _, sn := range snaps {
		out = append(out, sn.Info())
	}
	return out, nil
}

// LiveLogicalUsage returns per-service live bytes and per-mailbox totals.
func (s *Store) LiveLogicalUsage(ctx context.Context) (byService map[string]int64, topUsers []storage.UserUsage, err error) {
	byService = map[string]int64{}
	rows, err := s.DB.SQL.QueryContext(ctx, `
		SELECT service, mailbox, COALESCE(SUM(size),0) FROM catalog_items
		WHERE tenant_id=? AND deleted=0
		GROUP BY service, mailbox`, s.TenantID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var users []storage.UserUsage
	for rows.Next() {
		var svc, mb string
		var n int64
		if err := rows.Scan(&svc, &mb, &n); err != nil {
			return nil, nil, err
		}
		byService[svc] += n
		if mb == "" || n == 0 {
			continue
		}
		users = append(users, storage.UserUsage{
			User: mb, Service: svc, Bytes: n,
			Human: storage.FormatBytes(n), GB: storage.BytesToGB(n),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	for i := 0; i < len(users); i++ {
		for j := i + 1; j < len(users); j++ {
			if users[j].Bytes > users[i].Bytes {
				users[i], users[j] = users[j], users[i]
			}
		}
	}
	if len(users) > 20 {
		users = users[:20]
	}
	return byService, users, nil
}

func (s *Store) SnapshotCounts(ctx context.Context) (bytes map[string]int64, counts map[string]int, err error) {
	bytes = map[string]int64{}
	counts = map[string]int{}
	rows, err := s.DB.SQL.QueryContext(ctx, `
		SELECT service, COUNT(1), COALESCE(SUM(bytes_live),0) FROM catalog_snapshots
		WHERE tenant_id=? GROUP BY service`, s.TenantID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var svc string
		var n int
		var b int64
		if err := rows.Scan(&svc, &n, &b); err != nil {
			return nil, nil, err
		}
		counts[svc] = n
		bytes[svc] = b
	}
	return bytes, counts, rows.Err()
}
