package storage

import (
	"context"
	"sync"
	"time"
)

const snapListCacheTTL = 3 * time.Minute

type snapListEntry struct {
	at    time.Time
	snaps []SnapshotInfo
}

// Engine holds an in-memory snapshot-manifest cache so tenant pages / browser
// version lists do not reopen Kopia on every request.
func (e *Engine) snapCacheMap() *sync.Map {
	if e.snapLists == nil {
		e.snapMu.Lock()
		if e.snapLists == nil {
			e.snapLists = &sync.Map{}
		}
		e.snapMu.Unlock()
	}
	return e.snapLists
}

// InvalidateSnapshotCache drops cached snapshot lists for a tenant repo.
func (e *Engine) InvalidateSnapshotCache(repoPath string) {
	if e.snapLists == nil {
		return
	}
	e.snapLists.Delete(repoPath)
}

// ListSnapshotsCached returns ListSnapshots with a short TTL cache.
func (e *Engine) ListSnapshotsCached(ctx context.Context, repoPath, password string) ([]SnapshotInfo, error) {
	if v, ok := e.snapCacheMap().Load(repoPath); ok {
		ent := v.(snapListEntry)
		if time.Since(ent.at) < snapListCacheTTL {
			out := make([]SnapshotInfo, len(ent.snaps))
			copy(out, ent.snaps)
			return out, nil
		}
	}
	snaps, err := e.ListSnapshots(ctx, repoPath, password)
	if err != nil {
		return nil, err
	}
	cp := make([]SnapshotInfo, len(snaps))
	copy(cp, snaps)
	e.snapCacheMap().Store(repoPath, snapListEntry{at: time.Now(), snaps: cp})
	out := make([]SnapshotInfo, len(snaps))
	copy(out, snaps)
	return out, nil
}
