package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID           string
	Username     string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type APIToken struct {
	ID         string
	UserID     string
	Name       string
	Kind       string // user | env
	TokenHash  string
	Prefix     string
	Scope      string // read | write
	CreatedAt  time.Time
	LastUsedAt time.Time
}

func (d *DB) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	u := &User{}
	err := d.SQL.QueryRowContext(ctx, `
		SELECT id, username, password_hash, created_at, updated_at
		FROM users WHERE username=?`, username).Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (d *DB) GetUser(ctx context.Context, id string) (*User, error) {
	u := &User{}
	err := d.SQL.QueryRowContext(ctx, `
		SELECT id, username, password_hash, created_at, updated_at
		FROM users WHERE id=?`, id).Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (d *DB) UpsertUser(ctx context.Context, username, passwordHash string) (*User, error) {
	if username == "" || passwordHash == "" {
		return nil, fmt.Errorf("username and password hash required")
	}
	existing, err := d.GetUserByUsername(ctx, username)
	now := time.Now().UTC()
	if err == nil {
		_, err = d.SQL.ExecContext(ctx, `
			UPDATE users SET password_hash=?, updated_at=? WHERE id=?`,
			passwordHash, now, existing.ID)
		if err != nil {
			return nil, err
		}
		existing.PasswordHash = passwordHash
		existing.UpdatedAt = now
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	u := &User{
		ID: uuid.NewString(), Username: username, PasswordHash: passwordHash,
		CreatedAt: now, UpdatedAt: now,
	}
	_, err = d.SQL.ExecContext(ctx, `
		INSERT INTO users (id, username, password_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.PasswordHash, u.CreatedAt, u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (d *DB) ListAPITokens(ctx context.Context, userID string) ([]APIToken, error) {
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT id, user_id, name, kind, token_hash, prefix, scope, created_at, last_used_at
		FROM api_tokens WHERE user_id=? AND kind='user' ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (d *DB) GetAPITokenByHash(ctx context.Context, tokenHash string) (*APIToken, error) {
	if tokenHash == "" {
		return nil, sql.ErrNoRows
	}
	row := d.SQL.QueryRowContext(ctx, `
		SELECT id, user_id, name, kind, token_hash, prefix, scope, created_at, last_used_at
		FROM api_tokens WHERE token_hash=? AND kind='user'`, tokenHash)
	t, err := scanAPIToken(row)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (d *DB) InsertAPIToken(ctx context.Context, t *APIToken) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.Kind == "" {
		t.Kind = "user"
	}
	t.CreatedAt = time.Now().UTC()
	_, err := d.SQL.ExecContext(ctx, `
		INSERT INTO api_tokens (id, user_id, name, kind, token_hash, prefix, scope, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.UserID, t.Name, t.Kind, t.TokenHash, t.Prefix, t.Scope, t.CreatedAt)
	return err
}

func (d *DB) DeleteAPITokensByKind(ctx context.Context, userID, kind string) error {
	if userID == "" || kind == "" {
		return fmt.Errorf("user id and kind required")
	}
	_, err := d.SQL.ExecContext(ctx, `DELETE FROM api_tokens WHERE user_id=? AND kind=?`, userID, kind)
	return err
}

func (d *DB) DeleteAPIToken(ctx context.Context, userID, tokenID string) error {
	res, err := d.SQL.ExecContext(ctx, `
		DELETE FROM api_tokens WHERE id=? AND user_id=? AND kind='user'`, tokenID, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (d *DB) TouchAPIToken(ctx context.Context, id string) {
	_, _ = d.SQL.ExecContext(ctx, `UPDATE api_tokens SET last_used_at=? WHERE id=?`, time.Now().UTC(), id)
}

type tokenScanner interface {
	Scan(dest ...any) error
}

func scanAPIToken(s tokenScanner) (APIToken, error) {
	var t APIToken
	var last sql.NullTime
	err := s.Scan(&t.ID, &t.UserID, &t.Name, &t.Kind, &t.TokenHash, &t.Prefix, &t.Scope, &t.CreatedAt, &last)
	if last.Valid {
		t.LastUsedAt = last.Time
	}
	return t, err
}
