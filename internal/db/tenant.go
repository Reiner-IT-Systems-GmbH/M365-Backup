package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type Tenant struct {
	ID            string
	Name          string
	AzureTenantID string
	ClientID      string
	ClientSecret  string // encrypted at rest
	SecretExpires time.Time
	KopiaPassword string // encrypted at rest
	KopiaRepoPath string
	Status        string
	RetentionJSON string // Smart Recycle policy JSON
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (d *DB) CreateTenant(ctx context.Context, t *Tenant) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	t.CreatedAt = now
	t.UpdatedAt = now
	if t.Status == "" {
		t.Status = "setup"
	}
	_, err := d.SQL.ExecContext(ctx, `
		INSERT INTO tenants (id, name, azure_tenant_id, client_id, client_secret, secret_expires,
			kopia_password, kopia_repo_path, status, retention_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.AzureTenantID, t.ClientID, t.ClientSecret, NullTime(t.SecretExpires),
		t.KopiaPassword, t.KopiaRepoPath, t.Status, nullEmpty(t.RetentionJSON), t.CreatedAt, t.UpdatedAt,
	)
	return err
}

func (d *DB) UpdateTenant(ctx context.Context, t *Tenant) error {
	t.UpdatedAt = time.Now().UTC()
	_, err := d.SQL.ExecContext(ctx, `
		UPDATE tenants SET name=?, azure_tenant_id=?, client_id=?, client_secret=?, secret_expires=?,
			kopia_password=?, kopia_repo_path=?, status=?, retention_json=?, updated_at=?
		WHERE id=?`,
		t.Name, t.AzureTenantID, t.ClientID, t.ClientSecret, NullTime(t.SecretExpires),
		t.KopiaPassword, t.KopiaRepoPath, t.Status, nullEmpty(t.RetentionJSON), t.UpdatedAt, t.ID,
	)
	return err
}

func (d *DB) UpdateTenantRetention(ctx context.Context, tenantID, retentionJSON string) error {
	_, err := d.SQL.ExecContext(ctx, `
		UPDATE tenants SET retention_json=?, updated_at=? WHERE id=?`,
		nullEmpty(retentionJSON), time.Now().UTC(), tenantID,
	)
	return err
}

func (d *DB) DeleteTenant(ctx context.Context, id string) error {
	_, err := d.SQL.ExecContext(ctx, `DELETE FROM tenants WHERE id=?`, id)
	return err
}

func (d *DB) GetTenant(ctx context.Context, id string) (*Tenant, error) {
	row := d.SQL.QueryRowContext(ctx, `
		SELECT id, name, azure_tenant_id, client_id, client_secret, secret_expires,
			kopia_password, kopia_repo_path, status, COALESCE(retention_json,''), created_at, updated_at
		FROM tenants WHERE id=?`, id)
	return scanTenant(row)
}

func (d *DB) ListTenants(ctx context.Context) ([]Tenant, error) {
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT id, name, azure_tenant_id, client_id, client_secret, secret_expires,
			kopia_password, kopia_repo_path, status, COALESCE(retention_json,''), created_at, updated_at
		FROM tenants ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tenant
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

type scannable interface {
	Scan(dest ...any) error
}

func scanTenant(row scannable) (*Tenant, error) {
	var t Tenant
	var exp sql.NullTime
	var retention sql.NullString
	err := row.Scan(&t.ID, &t.Name, &t.AzureTenantID, &t.ClientID, &t.ClientSecret, &exp,
		&t.KopiaPassword, &t.KopiaRepoPath, &t.Status, &retention, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if exp.Valid {
		t.SecretExpires = exp.Time
	}
	if retention.Valid {
		t.RetentionJSON = retention.String
	}
	return &t, nil
}

func nullEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
