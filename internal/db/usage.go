package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/rhw/m365backup/internal/storage"
)

// TenantUsageRow is a cached MeasureUsage result.
type TenantUsageRow struct {
	TenantID   string
	TotalBytes int64
	Report     *storage.UsageReport
	MeasuredAt time.Time
	UpdatedAt  time.Time
}

func (d *DB) UpsertTenantUsage(ctx context.Context, tenantID string, report *storage.UsageReport) error {
	if report == nil {
		return nil
	}
	report.TenantID = tenantID
	if report.MeasuredAt.IsZero() {
		report.MeasuredAt = time.Now().UTC()
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if d.Driver == DriverMySQL {
		_, err = d.SQL.ExecContext(ctx, `
			INSERT INTO tenant_usage (tenant_id, total_bytes, report_json, measured_at, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE total_bytes=VALUES(total_bytes), report_json=VALUES(report_json),
				measured_at=VALUES(measured_at), updated_at=VALUES(updated_at)`,
			tenantID, report.TotalBytes, string(raw), report.MeasuredAt, now,
		)
		return err
	}
	_, err = d.SQL.ExecContext(ctx, `
		INSERT INTO tenant_usage (tenant_id, total_bytes, report_json, measured_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id) DO UPDATE SET
			total_bytes=excluded.total_bytes,
			report_json=excluded.report_json,
			measured_at=excluded.measured_at,
			updated_at=excluded.updated_at`,
		tenantID, report.TotalBytes, string(raw), report.MeasuredAt, now,
	)
	return err
}

func (d *DB) GetTenantUsage(ctx context.Context, tenantID string) (*TenantUsageRow, error) {
	row := d.SQL.QueryRowContext(ctx, `
		SELECT tenant_id, total_bytes, report_json, measured_at, updated_at
		FROM tenant_usage WHERE tenant_id=?`, tenantID)
	u, err := scanTenantUsage(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (d *DB) ListTenantUsage(ctx context.Context) (map[string]*TenantUsageRow, error) {
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT tenant_id, total_bytes, report_json, measured_at, updated_at FROM tenant_usage`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*TenantUsageRow{}
	for rows.Next() {
		u, err := scanTenantUsageRows(rows)
		if err != nil {
			return nil, err
		}
		out[u.TenantID] = u
	}
	return out, rows.Err()
}

func scanTenantUsage(row *sql.Row) (*TenantUsageRow, error) {
	var u TenantUsageRow
	var raw string
	if err := row.Scan(&u.TenantID, &u.TotalBytes, &raw, &u.MeasuredAt, &u.UpdatedAt); err != nil {
		return nil, err
	}
	u.Report = &storage.UsageReport{}
	if err := json.Unmarshal([]byte(raw), u.Report); err != nil {
		return nil, err
	}
	u.Report.TenantID = u.TenantID
	return &u, nil
}

func scanTenantUsageRows(rows *sql.Rows) (*TenantUsageRow, error) {
	var u TenantUsageRow
	var raw string
	if err := rows.Scan(&u.TenantID, &u.TotalBytes, &raw, &u.MeasuredAt, &u.UpdatedAt); err != nil {
		return nil, err
	}
	u.Report = &storage.UsageReport{}
	if err := json.Unmarshal([]byte(raw), u.Report); err != nil {
		return nil, err
	}
	u.Report.TenantID = u.TenantID
	return &u, nil
}
