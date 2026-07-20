package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

//go:embed migrations/sqlite/*.sql migrations/mysql/*.sql
var migrationsFS embed.FS

type Driver string

const (
	DriverSQLite Driver = "sqlite"
	DriverMySQL  Driver = "mysql"
)

type Options struct {
	Driver     Driver
	SQLitePath string
	MySQLDSN   string // user:pass@tcp(host:3306)/dbname?parseTime=true&...
}

type DB struct {
	SQL    *sql.DB
	Driver Driver
}

func Open(opts Options) (*DB, error) {
	switch opts.Driver {
	case DriverMySQL, "mariadb":
		return openMySQL(opts.MySQLDSN)
	case DriverSQLite, "":
		return openSQLite(opts.SQLitePath)
	default:
		return nil, fmt.Errorf("unsupported DB_DRIVER %q (use sqlite or mysql)", opts.Driver)
	}
}

func openSQLite(path string) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("DATABASE_PATH is required for sqlite")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", path)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	d := &DB{SQL: sqlDB, Driver: DriverSQLite}
	if err := d.migrate(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return d, nil
}

func openMySQL(dsn string) (*DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("MYSQL_DSN or MYSQL_* settings are required for mysql")
	}
	if !strings.Contains(dsn, "parseTime") {
		if strings.Contains(dsn, "?") {
			dsn += "&parseTime=true&loc=UTC"
		} else {
			dsn += "?parseTime=true&loc=UTC"
		}
	}
	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)
	for i := 0; i < 30; i++ {
		if err := sqlDB.Ping(); err == nil {
			break
		} else if i == 29 {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("mysql ping: %w", err)
		}
		time.Sleep(time.Second)
	}
	d := &DB{SQL: sqlDB, Driver: DriverMySQL}
	if err := d.migrate(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) Close() error {
	return d.SQL.Close()
}

func (d *DB) migrate() error {
	if _, err := d.SQL.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INT PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return err
	}
	dir := "migrations/sqlite"
	if d.Driver == DriverMySQL {
		dir = "migrations/mysql"
	}
	versions := []struct {
		n    int
		file string
	}{
		{1, dir + "/001_init.sql"},
		{2, dir + "/002_job_logs.sql"},
		{3, dir + "/003_job_progress.sql"},
		{4, dir + "/004_retention.sql"},
		{5, dir + "/005_usage_cache.sql"},
	}
	for _, v := range versions {
		var n int
		if err := d.SQL.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, v.n).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, err := migrationsFS.ReadFile(v.file)
		if err != nil {
			return fmt.Errorf("read migration %d: %w", v.n, err)
		}
		tx, err := d.SQL.Begin()
		if err != nil {
			return err
		}
		for _, stmt := range splitSQL(string(body)) {
			if _, err := tx.Exec(stmt); err != nil {
				// MySQL DDL auto-commits; a retry must tolerate "column already exists".
				if isDuplicateColumnErr(err) {
					continue
				}
				_ = tx.Rollback()
				return fmt.Errorf("apply migration %d: %w\nstmt: %s", v.n, err, truncate(stmt, 120))
			}
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES (?)`, v.n); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func isDuplicateColumnErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "duplicate column") ||
		strings.Contains(s, "1060") ||
		(strings.Contains(s, "already exists") && strings.Contains(s, "column"))
}

func splitSQL(s string) []string {
	parts := strings.Split(s, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		lines := strings.Split(p, "\n")
		var keep []string
		for _, line := range lines {
			trim := strings.TrimSpace(line)
			if trim == "" || strings.HasPrefix(trim, "--") {
				continue
			}
			keep = append(keep, line)
		}
		stmt := strings.TrimSpace(strings.Join(keep, "\n"))
		if stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func (d *DB) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func NullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

func NullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
