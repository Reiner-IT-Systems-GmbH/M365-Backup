package backup

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/storage"
	"github.com/rhw/m365backup/internal/tenant"
)

// UsageScanner walks tenant repos (du-style) and stores results in tenant_usage.
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

// Running reports whether a full or partial scan is in progress.
func (u *UsageScanner) Running() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.running
}

// RefreshAll measures every tenant. Returns false if a scan is already running.
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

// RefreshTenant measures one tenant synchronously (or waits if a full scan holds the lock briefly).
func (u *UsageScanner) RefreshTenant(ctx context.Context, tenantID string) error {
	t, err := u.DB.GetTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	u.mu.Lock()
	if u.running {
		u.mu.Unlock()
		// Still allow single-tenant refresh while full scan runs — measure without global lock.
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
	_, pass, err := u.Tenants.DecryptSecrets(t)
	if err != nil {
		return err
	}
	snaps, _ := u.Store.ListSnapshots(ctx, t.KopiaRepoPath, pass)
	jobs, _ := u.DB.ListJobs(ctx, t.ID, 200)
	m := map[string]string{}
	for _, j := range jobs {
		if j.KopiaSnapshot != "" && j.Service != "" {
			m[j.KopiaSnapshot] = j.Service
		}
	}
	storage.AnnotateServices(snaps, m)
	report, err := u.Store.MeasureUsage(t.KopiaRepoPath, snaps)
	if err != nil {
		return err
	}
	return u.DB.UpsertTenantUsage(ctx, t.ID, report)
}
