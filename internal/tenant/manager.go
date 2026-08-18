package tenant

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rhw/m365backup/internal/crypto"
	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/storage"
)

var DefaultSchedules = []struct {
	Service  string
	CronExpr string
	Enabled  bool
}{
	// Staggered so services do not all fire in the same minute.
	{"exchange", "0 * * * *", true},    // hourly at :00
	{"onedrive", "30 2 * * *", true},   // daily 02:30
	{"teams", "0 3 * * *", true},       // daily 03:00
	{"sharepoint", "30 3 * * 0", true}, // weekly Sun 03:30
	{"pst", "0 5 * * 0", false},        // weekly Sun 05:00, off by default
}

// DefaultCronFor returns the built-in cron expression for a service.
func DefaultCronFor(service string) (string, bool) {
	for _, ds := range DefaultSchedules {
		if ds.Service == service {
			return ds.CronExpr, true
		}
	}
	return "", false
}

// EnsureDefaultSchedules creates any missing per-service schedule rows for a tenant.
// Existing rows are left unchanged (cron/enabled stay as configured).
func (m *Manager) EnsureDefaultSchedules(ctx context.Context, tenantID string) (created int, err error) {
	existing, err := m.DB.ListSchedules(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	have := map[string]bool{}
	for _, s := range existing {
		have[strings.ToLower(s.Service)] = true
	}
	for _, ds := range DefaultSchedules {
		if have[ds.Service] {
			continue
		}
		s := &db.Schedule{
			TenantID: tenantID,
			Service:  ds.Service,
			CronExpr: ds.CronExpr,
			Enabled:  ds.Enabled,
		}
		if err := m.DB.CreateSchedule(ctx, s); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

type Manager struct {
	DB        *db.DB
	Cipher    *crypto.Cipher
	StoreRoot string
	Store     *storage.Engine
	BaseURL   string
}

type CreateInput struct {
	Name          string
	AzureTenantID string
	ClientID      string
	ClientSecret  string
	SecretExpires time.Time
}

func (m *Manager) Create(ctx context.Context, in CreateInput) (*db.Tenant, error) {
	encSecret, err := m.Cipher.Encrypt(in.ClientSecret)
	if err != nil {
		return nil, err
	}
	storePass, err := crypto.RandomPassword(32)
	if err != nil {
		return nil, err
	}
	encStore, err := m.Cipher.Encrypt(storePass)
	if err != nil {
		return nil, err
	}

	t := &db.Tenant{
		Name:          in.Name,
		AzureTenantID: in.AzureTenantID,
		ClientID:      in.ClientID,
		ClientSecret:  encSecret,
		SecretExpires: in.SecretExpires,
		StorePassword: encStore,
		Status:        "setup",
	}
	// provisional ID for path; CreateTenant assigns UUID before insert if empty —
	// we need ID first for store path
	if err := m.DB.CreateTenant(ctx, t); err != nil {
		return nil, err
	}
	t.StorePath = filepath.Join(m.StoreRoot, t.ID)
	if err := os.MkdirAll(t.StorePath, 0o700); err != nil {
		return nil, err
	}
	if err := m.Store.CreateRepo(ctx, t.StorePath, storePass); err != nil {
		return nil, fmt.Errorf("create store: %w", err)
	}
	if err := m.DB.UpdateTenant(ctx, t); err != nil {
		return nil, err
	}
	for _, ds := range DefaultSchedules {
		s := &db.Schedule{
			TenantID: t.ID,
			Service:  ds.Service,
			CronExpr: ds.CronExpr,
			Enabled:  ds.Enabled,
		}
		if err := m.DB.CreateSchedule(ctx, s); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func (m *Manager) DecryptSecrets(t *db.Tenant) (clientSecret, storePassword string, err error) {
	clientSecret, err = m.Cipher.Decrypt(t.ClientSecret)
	if err != nil {
		return "", "", err
	}
	storePassword, err = m.Cipher.Decrypt(t.StorePassword)
	if err != nil {
		return "", "", err
	}
	return clientSecret, storePassword, nil
}

func (m *Manager) ConsentURL(t *db.Tenant, state string) string {
	u := fmt.Sprintf("https://login.microsoftonline.com/%s/adminconsent", url.PathEscape(t.AzureTenantID))
	q := url.Values{}
	q.Set("client_id", t.ClientID)
	q.Set("redirect_uri", m.BaseURL+"/api/consent/callback")
	q.Set("state", state)
	return u + "?" + q.Encode()
}

func (m *Manager) Activate(ctx context.Context, tenantID string) error {
	t, err := m.DB.GetTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	t.Status = "active"
	return m.DB.UpdateTenant(ctx, t)
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	return m.DB.DeleteTenant(ctx, id)
}

// BindStorePath forces t.StorePath to {StoreRoot}/{tenantID} (the Docker volume).
// Tenants created with an old host path otherwise write into the container overlay on /.
func (m *Manager) BindStorePath(ctx context.Context, t *db.Tenant) (previous string, err error) {
	if m == nil || t == nil || t.ID == "" {
		return "", nil
	}
	if m.StoreRoot == "" {
		return t.StorePath, nil
	}
	want := filepath.Clean(filepath.Join(m.StoreRoot, t.ID))
	if _, err := storage.GuardPath(want); err != nil {
		return t.StorePath, err
	}
	previous = t.StorePath
	if filepath.Clean(previous) == want {
		return previous, os.MkdirAll(want, 0o700)
	}
	if err := os.MkdirAll(want, 0o700); err != nil {
		return previous, err
	}
	t.StorePath = want
	if m.DB != nil {
		if err := m.DB.UpdateTenant(ctx, t); err != nil {
			t.StorePath = previous
			return previous, err
		}
	}
	return previous, nil
}

// RebaseAllStorePaths rewrites every tenant onto StoreRoot. Returns how many rows changed.
func (m *Manager) RebaseAllStorePaths(ctx context.Context) (int, error) {
	if m == nil || m.DB == nil || m.StoreRoot == "" {
		return 0, nil
	}
	list, err := m.DB.ListTenants(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range list {
		t := &list[i]
		old := t.StorePath
		if _, err := m.BindStorePath(ctx, t); err != nil {
			return n, fmt.Errorf("tenant %s: %w", t.ID, err)
		}
		if filepath.Clean(old) != filepath.Clean(t.StorePath) {
			n++
		}
	}
	return n, nil
}
