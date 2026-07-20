package storage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kopia/kopia/fs/localfs"
	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/blob/filesystem"
	"github.com/kopia/kopia/repo/content"
	"github.com/kopia/kopia/repo/manifest"
	"github.com/kopia/kopia/snapshot"
	"github.com/kopia/kopia/snapshot/policy"
	"github.com/kopia/kopia/snapshot/restore"
	"github.com/kopia/kopia/snapshot/snapshotfs"
	"github.com/kopia/kopia/snapshot/upload"
)

const (
	kopiaHostName = "m365backup"
	serviceTagKey = "m365-service"
)

// Engine manages per-tenant Kopia repositories on the local filesystem.
//
// Layout under {KOPIA_ROOT}/{tenant-id}/:
//
//	repo/           — Kopia filesystem repository (restorable with upstream `kopia`)
//	kopia.config    — local connection config
//	.kopia-cache/   — content cache
//	sync/           — live Graph sync trees (not inside the Kopia repo)
//	exports/        — PST export runs
//
// Recovery requires the repo/ directory and the tenant repository password — not this app or the DB.
type Engine struct{}

func NewEngine() *Engine { return &Engine{} }

type SnapshotInfo struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Source    string    `json:"source"`
	Service   string    `json:"service,omitempty"`
	Bytes     int64     `json:"bytes"`
	Files     int       `json:"files"`
}

func (e *Engine) CreateRepo(ctx context.Context, repoPath, password string) error {
	if err := rejectLegacyRepo(repoPath); err != nil {
		return err
	}
	dataDir := RepoDataDir(repoPath)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(RepoCacheDir(repoPath), 0o700); err != nil {
		return err
	}

	st, err := filesystem.New(ctx, &filesystem.Options{Path: dataDir}, true)
	if err != nil {
		return fmt.Errorf("kopia storage: %w", err)
	}
	defer st.Close(ctx)

	if err := repo.Initialize(ctx, st, &repo.NewRepositoryOptions{}, password); err != nil {
		if !errors.Is(err, repo.ErrAlreadyInitialized) {
			return fmt.Errorf("kopia initialize: %w", err)
		}
	}
	return e.connectRepo(ctx, repoPath, password)
}

func (e *Engine) Snapshot(ctx context.Context, repoPath, password, sourceDir, service string) (*SnapshotInfo, error) {
	if service == "" {
		service = InferServiceFromSource(sourceDir)
	}
	if service == "" {
		service = "unknown"
	}
	absSource, err := filepath.Abs(sourceDir)
	if err != nil {
		return nil, err
	}

	var info *SnapshotInfo
	err = e.withRepo(ctx, repoPath, password, func(ctx context.Context, rep repo.Repository) error {
		return repo.WriteSession(ctx, rep, repo.WriteSessionOptions{Purpose: "m365-snapshot"}, func(ctx context.Context, w repo.RepositoryWriter) error {
			entry, err := localfs.Directory(absSource)
			if err != nil {
				return fmt.Errorf("open source dir: %w", err)
			}
			src := snapshot.SourceInfo{
				Host:     kopiaHostName,
				UserName: service,
				Path:     absSource,
			}
			u := upload.NewUploader(w)
			man, err := u.Upload(ctx, entry, policy.BuildTree(nil, policy.DefaultPolicy), src)
			if err != nil {
				return fmt.Errorf("kopia upload: %w", err)
			}
			man.Tags = map[string]string{serviceTagKey: service}
			id, err := snapshot.SaveSnapshot(ctx, w, man)
			if err != nil {
				return fmt.Errorf("kopia save snapshot: %w", err)
			}
			info = manifestToInfo(man, id)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return info, nil
}

// InferServiceFromSource guesses exchange|onedrive|teams|sharepoint from a sync/stage path.
func InferServiceFromSource(source string) string {
	s := strings.ToLower(filepath.ToSlash(source))
	switch {
	case strings.Contains(s, "/sync/exchange"), strings.HasSuffix(s, "/exchange"):
		return "exchange"
	case strings.Contains(s, "/sync/onedrive"), strings.HasSuffix(s, "/onedrive"):
		return "onedrive"
	case strings.Contains(s, "/teams"), strings.HasSuffix(s, "/teams"):
		return "teams"
	case strings.Contains(s, "/sharepoint"), strings.HasSuffix(s, "/sharepoint"):
		return "sharepoint"
	default:
		return ""
	}
}

func (e *Engine) Restore(ctx context.Context, repoPath, password, snapshotID, destDir string) error {
	if err := ValidateSnapshotID(snapshotID); err != nil {
		return err
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return err
	}
	return e.withRepo(ctx, repoPath, password, func(ctx context.Context, rep repo.Repository) error {
		man, err := snapshot.LoadSnapshot(ctx, rep, manifest.ID(snapshotID))
		if err != nil {
			return fmt.Errorf("load snapshot: %w", err)
		}
		root, err := snapshotfs.SnapshotRoot(rep, man)
		if err != nil {
			return fmt.Errorf("snapshot root: %w", err)
		}
		out := &restore.FilesystemOutput{
			TargetPath:             destDir,
			OverwriteDirectories:   true,
			OverwriteFiles:         true,
			OverwriteSymlinks:      true,
			IgnorePermissionErrors: true,
			SkipOwners:             true,
			SkipPermissions:        true,
			SkipTimes:              true,
		}
		if err := out.Init(ctx); err != nil {
			return fmt.Errorf("kopia restore init: %w", err)
		}
		_, err = restore.Entry(ctx, rep, out, root, restore.Options{
			Parallel:               4,
			RestoreDirEntryAtDepth: math.MaxInt32,
		})
		if err != nil {
			return fmt.Errorf("kopia restore: %w", err)
		}
		return nil
	})
}

func (e *Engine) ListSnapshots(ctx context.Context, repoPath, password string) ([]SnapshotInfo, error) {
	if _, err := os.Stat(RepoDataDir(repoPath)); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []SnapshotInfo
	err := e.withRepo(ctx, repoPath, password, func(ctx context.Context, rep repo.Repository) error {
		ids, err := snapshot.ListSnapshotManifests(ctx, rep, nil, nil)
		if err != nil {
			return err
		}
		mans, err := snapshot.LoadSnapshots(ctx, rep, ids)
		if err != nil {
			return err
		}
		for _, man := range mans {
			if man == nil || man.IncompleteReason != "" {
				continue
			}
			out = append(out, *manifestToInfo(man, man.ID))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (e *Engine) connectRepo(ctx context.Context, repoPath, password string) error {
	dataDir := RepoDataDir(repoPath)
	st, err := filesystem.New(ctx, &filesystem.Options{Path: dataDir}, false)
	if err != nil {
		return fmt.Errorf("kopia storage open: %w", err)
	}
	defer st.Close(ctx)

	if err := os.MkdirAll(RepoCacheDir(repoPath), 0o700); err != nil {
		return err
	}
	cfg := RepoConfigFile(repoPath)
	opt := &repo.ConnectOptions{
		CachingOptions: content.CachingOptions{
			CacheDirectory:         RepoCacheDir(repoPath),
			ContentCacheSizeBytes:  100 << 20,
			MetadataCacheSizeBytes: 100 << 20,
		},
	}
	if err := repo.Connect(ctx, cfg, st, password, opt); err != nil {
		return fmt.Errorf("kopia connect: %w", err)
	}
	return nil
}

func (e *Engine) withRepo(ctx context.Context, repoPath, password string, fn func(context.Context, repo.Repository) error) error {
	if err := rejectLegacyRepo(repoPath); err != nil {
		return err
	}
	cfg := RepoConfigFile(repoPath)
	if _, err := os.Stat(cfg); err != nil {
		if err := e.connectRepo(ctx, repoPath, password); err != nil {
			return err
		}
	}
	rep, err := repo.Open(ctx, cfg, password, &repo.Options{
		OnFatalError: func(error) {},
	})
	if err != nil {
		if cerr := e.connectRepo(ctx, repoPath, password); cerr != nil {
			return fmt.Errorf("kopia open: %w (reconnect: %v)", err, cerr)
		}
		rep, err = repo.Open(ctx, cfg, password, &repo.Options{
			OnFatalError: func(error) {},
		})
		if err != nil {
			return fmt.Errorf("kopia open: %w", err)
		}
	}
	defer rep.Close(ctx)
	return fn(ctx, rep)
}

func rejectLegacyRepo(repoPath string) error {
	if _, err := os.Stat(filepath.Join(repoPath, "repo.json")); err == nil {
		return fmt.Errorf("legacy encrypted-tar repository at %s (repo.json); remove or migrate before using the Kopia backend", repoPath)
	}
	return nil
}

func manifestToInfo(man *snapshot.Manifest, id manifest.ID) *SnapshotInfo {
	svc := ""
	if man.Tags != nil {
		svc = man.Tags[serviceTagKey]
	}
	if svc == "" {
		svc = man.Source.UserName
	}
	if svc == "" {
		svc = InferServiceFromSource(man.Source.Path)
	}
	created := man.StartTime.ToTime().UTC()
	if created.IsZero() {
		created = man.EndTime.ToTime().UTC()
	}
	return &SnapshotInfo{
		ID:        string(id),
		CreatedAt: created,
		Source:    man.Source.Path,
		Service:   svc,
		Bytes:     man.Stats.TotalFileSize,
		Files:     int(man.Stats.TotalFileCount),
	}
}
