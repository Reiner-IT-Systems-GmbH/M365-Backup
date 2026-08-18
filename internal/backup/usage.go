package backup

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/rhw/m365backup/internal/catalog"
	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/storage"
	"github.com/rhw/m365backup/internal/tenant"
)

// UsageScanner walks tenant store dirs (du-style) and stores results in tenant_usage.
type UsageScanner struct {
	DB      *db.DB
	Store   *storage.Engine
	Tenants *tenant.Manager
	Log     *slog.Logger

	mu      sync.Mutex
	running bool
}

func NewUsageScanner(database *db.DB, store *storage.Engine, tenants *tenant.Manager, log *slog.Logger) *UsageScanner {
	return &UsageScanner{DB: database, Store: store, Tenants: tenants, Log: log}
}

func (u *UsageScanner) Running() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.running
}

func (u *UsageScanner) RefreshAll(ctx context.Context) (started bool) {
	_ = ctx
	u.mu.Lock()
	if u.running {
		u.mu.Unlock()
		return false
	}
	u.running = true
	u.mu.Unlock()

	go func() {
		defer func() {
			u.mu.Lock()
			u.running = false
			u.mu.Unlock()
		}()
		cctx := context.Background()
		start := time.Now()
		list, err := u.DB.ListTenants(cctx)
		if err != nil {
			u.Log.Error("usage scan list tenants", "err", err)
			return
		}
		u.Log.Info("usage scan start", "tenants", len(list))
		ok := 0
		for i := range list {
			if err := u.refreshOne(cctx, &list[i]); err != nil {
				u.Log.Warn("usage scan tenant", "tenant", list[i].Name, "err", err)
				continue
			}
			ok++
		}
		u.Log.Info("usage scan done", "ok", ok, "total", len(list), "took", time.Since(start).Round(time.Millisecond))
	}()
	return true
}

func (u *UsageScanner) RefreshTenant(ctx context.Context, tenantID string) error {
	t, err := u.DB.GetTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	u.mu.Lock()
	if u.running {
		u.mu.Unlock()
		return u.refreshOne(ctx, t)
	}
	u.running = true
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.running = false
		u.mu.Unlock()
	}()
	return u.refreshOne(ctx, t)
}

func (u *UsageScanner) refreshOne(ctx context.Context, t *db.Tenant) error {
	if u.Tenants != nil {
		if _, err := u.Tenants.BindStorePath(ctx, t); err != nil {
			return err
		}
	}
	_, pass, err := u.Tenants.DecryptSecrets(t)
	if err != nil {
		return err
	}
	cat, err := catalog.Open(u.DB, t.ID, t.StorePath, pass)
	if err != nil {
		return err
	}
	snaps, _ := cat.SnapshotInfos(ctx, "")
	live, top, _ := cat.LiveLogicalUsage(ctx)
	snapBytes, snapCount, _ := cat.SnapshotCounts(ctx)
	report, err := u.Store.MeasureUsageEx(t.StorePath, snaps, storage.UsageExtras{
		LiveByService: live,
		SnapByService: snapBytes,
		SnapCount:     snapCount,
		TopUsers:      top,
	})
	if err != nil {
		return err
	}
	return u.DB.UpsertTenantUsage(ctx, t.ID, report)
}
