package db

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type DeltaToken struct {
	ID        string
	TenantID  string
	Service   string
	UserID    string
	Token     string
	UpdatedAt time.Time
}

func (d *DB) UpsertDeltaToken(ctx context.Context, t DeltaToken) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	t.UpdatedAt = time.Now().UTC()
	var q string
	if d.Driver == DriverMySQL {
		q = `
		INSERT INTO delta_tokens (id, tenant_id, service, user_id, token, updated_at)
		VALUES (?, ?, ?, ?, ?, ?) AS new
		ON DUPLICATE KEY UPDATE token=new.token, updated_at=new.updated_at`
	} else {
		q = `
		INSERT INTO delta_tokens (id, tenant_id, service, user_id, token, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, service, user_id) DO UPDATE SET
			token=excluded.token, updated_at=excluded.updated_at`
	}
	_, err := d.SQL.ExecContext(ctx, q, t.ID, t.TenantID, t.Service, t.UserID, t.Token, t.UpdatedAt)
	return err
}

func (d *DB) GetDeltaToken(ctx context.Context, tenantID, service, userID string) (string, error) {
	var token string
	err := d.SQL.QueryRowContext(ctx, `
		SELECT token FROM delta_tokens WHERE tenant_id=? AND service=? AND user_id=?`,
		tenantID, service, userID).Scan(&token)
	if err != nil {
		return "", err
	}
	return token, nil
}

type NotificationSetting struct {
	ID        string
	TenantID  string // empty = global
	Channel   string
	Enabled   bool
	Config    string // JSON
	NotifyOn  string // JSON array
	CreatedAt time.Time
}

type NotificationLog struct {
	ID        string
	TenantID  string
	Channel   string
	EventType string
	Subject   string
	SentAt    time.Time
	Success   bool
	Error     string
}

func (d *DB) ListNotificationSettings(ctx context.Context) ([]NotificationSetting, error) {
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT id, COALESCE(tenant_id,''), channel, enabled, config, notify_on, created_at
		FROM notification_settings ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotificationSetting
	for rows.Next() {
		var s NotificationSetting
		var en int
		if err := rows.Scan(&s.ID, &s.TenantID, &s.Channel, &en, &s.Config, &s.NotifyOn, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.Enabled = en == 1
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) UpsertNotificationSetting(ctx context.Context, s *NotificationSetting) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	s.CreatedAt = time.Now().UTC()
	var q string
	if d.Driver == DriverMySQL {
		q = `
		INSERT INTO notification_settings (id, tenant_id, channel, enabled, config, notify_on, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) AS new
		ON DUPLICATE KEY UPDATE
			tenant_id=new.tenant_id, channel=new.channel, enabled=new.enabled,
			config=new.config, notify_on=new.notify_on`
	} else {
		q = `
		INSERT INTO notification_settings (id, tenant_id, channel, enabled, config, notify_on, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			tenant_id=excluded.tenant_id, channel=excluded.channel, enabled=excluded.enabled,
			config=excluded.config, notify_on=excluded.notify_on`
	}
	_, err := d.SQL.ExecContext(ctx, q,
		s.ID, NullString(s.TenantID), s.Channel, boolToInt(s.Enabled), s.Config, s.NotifyOn, s.CreatedAt,
	)
	return err
}

func (d *DB) InsertNotificationLog(ctx context.Context, l *NotificationLog) error {
	if l.ID == "" {
		l.ID = uuid.NewString()
	}
	l.SentAt = time.Now().UTC()
	_, err := d.SQL.ExecContext(ctx, `
		INSERT INTO notification_log (id, tenant_id, channel, event_type, subject, sent_at, success, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		l.ID, NullString(l.TenantID), l.Channel, l.EventType, l.Subject, l.SentAt, boolToInt(l.Success), NullString(l.Error),
	)
	return err
}
