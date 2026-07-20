package tenant

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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
	{"exchange", "0 * * * *", true},
	{"onedrive", "0 2 * * *", true},
	{"teams", "15 2 * * *", true},
	{"sharepoint", "0 1 * * 0", true},
	{"pst", "0 4 * * 0", false}, // weekly PST export, off by default
}

type Manager struct {
	DB        *db.DB
	Cipher    *crypto.Cipher
	KopiaRoot string
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
	kopiaPass, err := crypto.RandomPassword(32)
	if err != nil {
		return nil, err
	}
	encKopia, err := m.Cipher.Encrypt(kopiaPass)
	if err != nil {
		return nil, err
	}

	t := &db.Tenant{
		Name:          in.Name,
		AzureTenantID: in.AzureTenantID,
		ClientID:      in.ClientID,
		ClientSecret:  encSecret,
		SecretExpires: in.SecretExpires,
		KopiaPassword: encKopia,
		Status:        "setup",
	}
	// provisional ID for path; CreateTenant assigns UUID before insert if empty —
	// we need ID first for repo path
	if err := m.DB.CreateTenant(ctx, t); err != nil {
		return nil, err
	}
	t.KopiaRepoPath = filepath.Join(m.KopiaRoot, t.ID)
	if err := os.MkdirAll(t.KopiaRepoPath, 0o700); err != nil {
		return nil, err
	}
	if err := m.Store.CreateRepo(ctx, t.KopiaRepoPath, kopiaPass); err != nil {
		return nil, fmt.Errorf("create kopia repo: %w", err)
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

func (m *Manager) DecryptSecrets(t *db.Tenant) (clientSecret, kopiaPassword string, err error) {
	clientSecret, err = m.Cipher.Decrypt(t.ClientSecret)
	if err != nil {
		return "", "", err
	}
	kopiaPassword, err = m.Cipher.Decrypt(t.KopiaPassword)
	if err != nil {
		return "", "", err
	}
	return clientSecret, kopiaPassword, nil
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
