package backup

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/storage"
)

func liveSyncService(jobService string) string {
	switch jobService {
	case "exchange", "onedrive":
		return jobService
	case "pst":
		return "exchange"
	default:
		return ""
	}
}

func liveSyncDir(repoPath, service string) (string, error) {
	p, err := storage.EnsureSubpath(repoPath, filepath.Join("sync", service))
	if err != nil {
		return "", err
	}
	return storage.GuardPath(p)
}

func dirHasData(root string) bool {
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

// hydrateLiveSync restores the last snapshot into sync/{service} when the
// working tree is empty (snapshot-only mode). No-op when KeepLiveSync is set
// or the tree already has files (resume after a crash).
func (r *Runner) hydrateLiveSync(ctx context.Context, t *db.Tenant, kopiaPass, jobService string, prog *Progress) error {
	if r.KeepLiveSync || r.Store == nil {
		return nil
	}
	svc := liveSyncService(jobService)
	if svc == "" {
		return nil
	}
	dest, err := liveSyncDir(t.KopiaRepoPath, svc)
	if err != nil {
		return err
	}
	if dirHasData(dest) {
		return nil
	}
	snaps, err := r.Store.ListSnapshots(ctx, t.KopiaRepoPath, kopiaPass)
	if err != nil {
		return fmt.Errorf("list snapshots for live-sync restore: %w", err)
	}
	storage.AnnotateServices(snaps, nil)
	latest := storage.LatestSnapshotForService(snaps, svc)
	if latest == nil {
		return os.MkdirAll(dest, 0o755)
	}
	if prog != nil {
		prog.Emit("info", fmt.Sprintf("restoring snapshot %s into working copy (not kept on disk after the job)…", latest.ID))
	}
	_ = os.RemoveAll(dest)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := r.Store.Restore(ctx, t.KopiaRepoPath, kopiaPass, latest.ID, dest); err != nil {
		return fmt.Errorf("restore live-sync from snapshot: %w", err)
	}
	return nil
}

func (r *Runner) discardLiveSync(repoPath, jobService string) {
	if r.KeepLiveSync {
		return
	}
	svc := liveSyncService(jobService)
	if svc == "" {
		return
	}
	dest, err := liveSyncDir(repoPath, svc)
	if err != nil {
		return
	}
	if err := os.RemoveAll(dest); err != nil && r.Log != nil {
		r.Log.Warn("discard live-sync", "path", dest, "err", err)
		return
	}
	if r.Log != nil {
		r.Log.Info("discarded live-sync working copy", "service", svc)
	}
}
