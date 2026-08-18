package catalog

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/storage"
)

var importServices = []string{"exchange", "onedrive", "teams", "sharepoint"}

// GraphBackupServices are the services that use Graph incremental/full jobs.
var GraphBackupServices = importServices

// EnsureMigrated imports sync/ as generation 1 when the catalog is empty,
// then deletes sync/. Legacy leftover dirs (repo/, kopia.config) are removed
// after a successful import or when the caller later commits the first snapshot.
func (s *Store) EnsureMigrated(ctx context.Context, jobService, jobID string) (imported bool, needGraphFull bool, err error) {
	hasSnap, err := s.HasSnapshots(ctx)
	if err != nil {
		return false, false, err
	}
	if hasSnap {
		return false, false, nil
	}
	syncRoot := filepath.Join(s.Root, "sync")
	if !dirHasFiles(syncRoot) {
		needGraphFull = true
		return false, true, nil
	}
	for _, svc := range importServices {
		n, err := s.importSyncService(ctx, svc)
		if err != nil {
			return false, false, fmt.Errorf("import sync/%s: %w", svc, err)
		}
		if n == 0 {
			continue
		}
		jid := ""
		if svc == jobService {
			jid = jobID
		}
		if _, err := s.CommitSnapshot(ctx, svc, jid); err != nil {
			return false, false, fmt.Errorf("commit import %s: %w", svc, err)
		}
		imported = true
	}
	if err := os.RemoveAll(syncRoot); err != nil {
		return imported, false, fmt.Errorf("remove sync/: %w", err)
	}
	RemoveLegacyDirs(s.Root)
	return imported, false, nil
}

// EmptyLocalData reports whether a Graph backup would have nothing local to
// increment from. serviceEmpty: no catalog rows/snapshots, no sync/{service}/,
// no manifests/{service}/. tenantEmpty: no catalog at all, no sync/, no blobs
// or manifests — the first job should full-sync every enabled Graph service.
func EmptyLocalData(ctx context.Context, database *db.DB, tenantID, root, service string) (serviceEmpty, tenantEmpty bool, err error) {
	if database == nil || tenantID == "" {
		return false, false, fmt.Errorf("empty local data: database and tenant required")
	}
	nAll, err := countSnapshots(ctx, database, tenantID, "")
	if err != nil {
		return false, false, err
	}
	liveAll, err := countLiveItems(ctx, database, tenantID, "")
	if err != nil {
		return false, false, err
	}
	syncAll := dirHasFiles(filepath.Join(root, "sync"))
	blobs := dirHasFiles(filepath.Join(root, "blobs"))
	manifests := dirHasFiles(filepath.Join(root, "manifests"))
	tenantEmpty = nAll == 0 && liveAll == 0 && !syncAll && !blobs && !manifests

	if service == "" {
		return tenantEmpty, tenantEmpty, nil
	}
	nSvc, err := countSnapshots(ctx, database, tenantID, service)
	if err != nil {
		return false, false, err
	}
	liveSvc, err := countLiveItems(ctx, database, tenantID, service)
	if err != nil {
		return false, false, err
	}
	syncSvc := dirHasFiles(filepath.Join(root, "sync", service))
	manSvc := dirHasFiles(filepath.Join(root, "manifests", service))
	serviceEmpty = nSvc == 0 && liveSvc == 0 && !syncSvc && !manSvc
	return serviceEmpty, tenantEmpty, nil
}

func countSnapshots(ctx context.Context, database *db.DB, tenantID, service string) (int, error) {
	var n int
	q := `SELECT COUNT(1) FROM catalog_snapshots WHERE tenant_id=?`
	args := []any{tenantID}
	if service != "" {
		q += ` AND service=?`
		args = append(args, service)
	}
	err := database.SQL.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

func countLiveItems(ctx context.Context, database *db.DB, tenantID, service string) (int, error) {
	var n int
	q := `SELECT COUNT(1) FROM catalog_items WHERE tenant_id=? AND deleted=0`
	args := []any{tenantID}
	if service != "" {
		q += ` AND service=?`
		args = append(args, service)
	}
	err := database.SQL.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

// RemoveLegacyDirs deletes leftover repo/, kopia.config, and .kopia-cache/ if present.
func RemoveLegacyDirs(tenantRoot string) {
	if _, err := storage.GuardPath(tenantRoot); err != nil {
		return
	}
	_ = os.RemoveAll(filepath.Join(tenantRoot, "repo"))
	_ = os.Remove(filepath.Join(tenantRoot, "kopia.config"))
	_ = os.RemoveAll(filepath.Join(tenantRoot, ".kopia-cache"))
}

func (s *Store) importSyncService(ctx context.Context, service string) (int, error) {
	base := filepath.Join(s.Root, "sync", service)
	st, err := os.Stat(base)
	if err != nil || !st.IsDir() {
		return 0, nil
	}
	n := 0
	err = filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d == nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if name == "BACKUP_META.txt" || name == "SNAPSHOT_ROOT.txt" || strings.HasPrefix(name, ".") {
			return nil
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if strings.Contains(rel, "..") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		it := itemFromSyncPath(service, rel, name)
		if it.GraphItemID == "" {
			return nil
		}
		if err := s.Put(ctx, it, data); err != nil {
			return err
		}
		n++
		return nil
	})
	return n, err
}

func itemFromSyncPath(service, rel, name string) Item {
	it := Item{Service: service, Name: name, ContentType: "application/octet-stream"}
	parts := strings.Split(rel, "/")
	if len(parts) == 0 {
		return it
	}
	it.Mailbox = parts[0]
	if len(parts) == 2 {
		it.ParentPath = ""
		it.Name = parts[1]
	} else if len(parts) >= 3 {
		it.ParentPath = strings.Join(parts[1:len(parts)-1], "/")
		it.Name = parts[len(parts)-1]
	}
	switch service {
	case "exchange":
		if short, ok := emlShortFromName(it.Name); ok {
			it.GraphItemID = ImportEMLPrefix + short
			it.ContentType = "message/rfc822"
			if subj, ok := storage.SubjectFromEMLFilename(it.Name); ok {
				it.Subject = subj
			}
		} else {
			it.GraphItemID = ImportPathPrefix + rel
		}
	default:
		it.GraphItemID = ImportPathPrefix + rel
	}
	return it
}

func emlShortFromName(name string) (string, bool) {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	i := strings.LastIndex(base, "__")
	if i < 0 || i+2 >= len(base) {
		return "", false
	}
	id := base[i+2:]
	if len(id) < 6 || len(id) > 16 {
		return "", false
	}
	return id, true
}

func dirHasFiles(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if !d.IsDir() {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}
