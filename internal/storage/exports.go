package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/maintenance"
	"github.com/kopia/kopia/repo/manifest"
	"github.com/kopia/kopia/snapshot/snapshotmaintenance"
)

// PSTExportRoot is {repo}/exports/pst
func PSTExportRoot(repoPath string) string {
	return filepath.Join(repoPath, "exports", "pst")
}

// PSTExportRun describes one completed (or in-progress) export batch.
type PSTExportRun struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Path      string    `json:"path"`
	Users     int       `json:"users"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	Human     string    `json:"human"`
	Zips      []string  `json:"zips,omitempty"` // downloadable *.zip basenames
}

// ListPSTExports lists export run directories newest first.
func ListPSTExports(repoPath string) ([]PSTExportRun, error) {
	root := PSTExportRoot(repoPath)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []PSTExportRun
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		dir := filepath.Join(root, id)
		run := PSTExportRun{ID: id, Path: dir}
		if meta, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err == nil {
			_ = json.Unmarshal(meta, &run)
			run.ID = id
			run.Path = dir
		} else {
			info, _ := e.Info()
			if info != nil {
				run.CreatedAt = info.ModTime().UTC()
			}
			run.Bytes, _ = DirSize(dir)
		}
		run.Human = FormatBytes(run.Bytes)
		ents, _ := os.ReadDir(dir)
		for _, fe := range ents {
			if fe.IsDir() {
				continue
			}
			name := fe.Name()
			if strings.HasSuffix(strings.ToLower(name), ".zip") {
				run.Zips = append(run.Zips, name)
			}
		}
		sort.Strings(run.Zips)
		out = append(out, run)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// ApplyPSTExportRetention deletes older export runs beyond keepN.
func ApplyPSTExportRetention(repoPath string, keepN int) error {
	if keepN < 1 {
		keepN = 5
	}
	runs, err := ListPSTExports(repoPath)
	if err != nil {
		return err
	}
	for i := keepN; i < len(runs); i++ {
		_ = os.RemoveAll(runs[i].Path)
	}
	return nil
}

// WritePSTManifest writes export run metadata.
func WritePSTManifest(dir string, run PSTExportRun) error {
	run.Human = FormatBytes(run.Bytes)
	b, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o600)
}

// SanitizeExportName turns a UPN into a safe directory/file name.
func SanitizeExportName(upn string) string {
	s := strings.TrimSpace(upn)
	s = strings.ReplaceAll(s, "@", "_at_")
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
	if s == "" {
		return "user"
	}
	return s
}

// ApplySmartRetention prunes snapshots per service using Synology-style Smart Recycle.
// Deletes are applied as Kopia manifest removals (grouped by service tag/username), then
// a full Kopia maintenance+GC run reclaims unreferenced content so disk usage drops.
func (e *Engine) ApplySmartRetention(ctx context.Context, repoPath, password string, policy RetentionPolicy) (deleted int, err error) {
	snaps, err := e.ListSnapshots(ctx, repoPath, password)
	if err != nil {
		return 0, err
	}
	bySvc := map[string][]SnapshotInfo{}
	for _, sn := range snaps {
		svc := strings.ToLower(sn.Service)
		if svc == "" {
			svc = InferServiceFromSource(sn.Source)
		}
		if svc == "" {
			svc = "unknown"
		}
		bySvc[svc] = append(bySvc[svc], sn)
	}
	now := time.Now().UTC()
	var toDelete []string
	for _, list := range bySvc {
		sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
		keep := SelectSmartKeepIDs(list, policy, now)
		for _, sn := range list {
			if keep[sn.ID] {
				continue
			}
			toDelete = append(toDelete, sn.ID)
		}
	}
	if len(toDelete) == 0 {
		return 0, nil
	}
	err = e.withRepo(ctx, repoPath, password, func(ctx context.Context, rep repo.Repository) error {
		return repo.WriteSession(ctx, rep, repo.WriteSessionOptions{Purpose: "m365-smart-recycle"}, func(ctx context.Context, w repo.RepositoryWriter) error {
			for _, id := range toDelete {
				if err := w.DeleteManifest(ctx, manifest.ID(id)); err != nil {
					return fmt.Errorf("delete snapshot %s: %w", id, err)
				}
				deleted++
			}
			return nil
		})
	})
	if err != nil {
		return deleted, err
	}
	// Reclaim blob space; SafetyNone is intentional after Smart Recycle (operator-approved deletes).
	if gcErr := e.runFullGC(ctx, repoPath, password); gcErr != nil {
		return deleted, fmt.Errorf("smart recycle deleted %d snapshots but kopia GC failed: %w", deleted, gcErr)
	}
	return deleted, nil
}

// ApplyRetention keeps the newest keepN snapshots (legacy count-based). Prefer ApplySmartRetention.
func (e *Engine) ApplyRetention(ctx context.Context, repoPath, password string, keepN int) error {
	if keepN < 1 {
		keepN = 30
	}
	snaps, err := e.ListSnapshots(ctx, repoPath, password)
	if err != nil {
		return err
	}
	if len(snaps) <= keepN {
		return nil
	}
	toDelete := snaps[keepN:]
	err = e.withRepo(ctx, repoPath, password, func(ctx context.Context, rep repo.Repository) error {
		return repo.WriteSession(ctx, rep, repo.WriteSessionOptions{Purpose: "m365-retention"}, func(ctx context.Context, w repo.RepositoryWriter) error {
			for _, sn := range toDelete {
				if err := w.DeleteManifest(ctx, manifest.ID(sn.ID)); err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		return err
	}
	return e.runFullGC(ctx, repoPath, password)
}

// runFullGC runs Kopia snapshot GC + full maintenance so deleted snapshot content is purged.
func (e *Engine) runFullGC(ctx context.Context, repoPath, password string) error {
	return e.withRepo(ctx, repoPath, password, func(ctx context.Context, rep repo.Repository) error {
		dr, ok := rep.(repo.DirectRepository)
		if !ok {
			return fmt.Errorf("repository does not support direct maintenance/GC")
		}
		return repo.DirectWriteSession(ctx, dr, repo.WriteSessionOptions{Purpose: "m365-smart-recycle-gc"}, func(ctx context.Context, dw repo.DirectRepositoryWriter) error {
			if err := snapshotmaintenance.Run(ctx, dw, maintenance.ModeFull, true, maintenance.SafetyNone); err != nil {
				return fmt.Errorf("kopia maintenance: %w", err)
			}
			return nil
		})
	})
}

// EnsurePSTExportDir creates exports/pst and returns a new run directory.
func EnsurePSTExportDir(repoPath string) (runID, runDir string, err error) {
	runID = time.Now().UTC().Format("20060102T150405Z")
	runDir = filepath.Join(PSTExportRoot(repoPath), runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return "", "", fmt.Errorf("mkdir pst export: %w", err)
	}
	return runID, runDir, nil
}
