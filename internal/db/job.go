package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type Schedule struct {
	ID        string
	TenantID  string
	Service   string
	CronExpr  string
	Enabled   bool
	LastRun   time.Time
	NextRun   time.Time
	CreatedAt time.Time
}

type Job struct {
	ID               string
	TenantID         string
	ScheduleID       string
	Service          string
	JobType          string
	Status           string
	StartedAt        time.Time
	FinishedAt       time.Time
	ItemsNew         int
	ItemsTotal       int
	BytesTransferred int64
	ErrorMessage     string
	KopiaSnapshot    string
	ProgressPct      int
	ProgressMessage  string
	CreatedAt        time.Time
}

func (d *DB) CreateSchedule(ctx context.Context, s *Schedule) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	s.CreatedAt = time.Now().UTC()
	_, err := d.SQL.ExecContext(ctx, `
		INSERT INTO schedules (id, tenant_id, service, cron_expr, enabled, last_run, next_run, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.TenantID, s.Service, s.CronExpr, boolToInt(s.Enabled), NullTime(s.LastRun), NullTime(s.NextRun), s.CreatedAt,
	)
	return err
}

func (d *DB) ListSchedules(ctx context.Context, tenantID string) ([]Schedule, error) {
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT id, tenant_id, service, cron_expr, enabled, last_run, next_run, created_at
		FROM schedules WHERE tenant_id=? ORDER BY service`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		var s Schedule
		var en int
		var last, next sql.NullTime
		if err := rows.Scan(&s.ID, &s.TenantID, &s.Service, &s.CronExpr, &en, &last, &next, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.Enabled = en == 1
		if last.Valid {
			s.LastRun = last.Time
		}
		if next.Valid {
			s.NextRun = next.Time
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) ListAllSchedules(ctx context.Context) ([]Schedule, error) {
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT id, tenant_id, service, cron_expr, enabled, last_run, next_run, created_at
		FROM schedules WHERE enabled=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		var s Schedule
		var en int
		var last, next sql.NullTime
		if err := rows.Scan(&s.ID, &s.TenantID, &s.Service, &s.CronExpr, &en, &last, &next, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.Enabled = en == 1
		if last.Valid {
			s.LastRun = last.Time
		}
		if next.Valid {
			s.NextRun = next.Time
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) UpdateSchedule(ctx context.Context, s *Schedule) error {
	_, err := d.SQL.ExecContext(ctx, `
		UPDATE schedules SET cron_expr=?, enabled=?, last_run=?, next_run=? WHERE id=?`,
		s.CronExpr, boolToInt(s.Enabled), NullTime(s.LastRun), NullTime(s.NextRun), s.ID,
	)
	return err
}

func (d *DB) GetSchedule(ctx context.Context, id string) (*Schedule, error) {
	row := d.SQL.QueryRowContext(ctx, `
		SELECT id, tenant_id, service, cron_expr, enabled, last_run, next_run, created_at
		FROM schedules WHERE id=?`, id)
	var s Schedule
	var en int
	var last, next sql.NullTime
	if err := row.Scan(&s.ID, &s.TenantID, &s.Service, &s.CronExpr, &en, &last, &next, &s.CreatedAt); err != nil {
		return nil, err
	}
	s.Enabled = en == 1
	if last.Valid {
		s.LastRun = last.Time
	}
	if next.Valid {
		s.NextRun = next.Time
	}
	return &s, nil
}

func (d *DB) CreateJob(ctx context.Context, j *Job) error {
	if j.ID == "" {
		j.ID = uuid.NewString()
	}
	j.CreatedAt = time.Now().UTC()
	_, err := d.SQL.ExecContext(ctx, `
		INSERT INTO jobs (id, tenant_id, schedule_id, service, job_type, status, started_at, finished_at,
			items_new, items_total, bytes_transferred, error_message, kopia_snapshot, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.TenantID, NullString(j.ScheduleID), j.Service, j.JobType, j.Status,
		NullTime(j.StartedAt), NullTime(j.FinishedAt), j.ItemsNew, j.ItemsTotal, j.BytesTransferred,
		NullString(j.ErrorMessage), NullString(j.KopiaSnapshot), j.CreatedAt,
	)
	return err
}

func (d *DB) UpdateJob(ctx context.Context, j *Job) error {
	_, err := d.SQL.ExecContext(ctx, `
		UPDATE jobs SET status=?, started_at=?, finished_at=?, items_new=?, items_total=?,
			bytes_transferred=?, error_message=?, kopia_snapshot=?, progress_pct=?, progress_message=? WHERE id=?`,
		j.Status, NullTime(j.StartedAt), NullTime(j.FinishedAt), j.ItemsNew, j.ItemsTotal,
		j.BytesTransferred, NullString(j.ErrorMessage), NullString(j.KopiaSnapshot),
		j.ProgressPct, j.ProgressMessage, j.ID,
	)
	return err
}

// UpdateJobProgress updates counters + progress while a job is running (does not touch status).
func (d *DB) UpdateJobProgress(ctx context.Context, j *Job) error {
	_, err := d.SQL.ExecContext(ctx, `
		UPDATE jobs SET items_new=?, items_total=?, bytes_transferred=?, progress_pct=?, progress_message=?
		WHERE id=?`,
		j.ItemsNew, j.ItemsTotal, j.BytesTransferred, j.ProgressPct, j.ProgressMessage, j.ID,
	)
	return err
}

func (d *DB) UpdateJobProgressMessage(ctx context.Context, jobID, msg string) error {
	_, err := d.SQL.ExecContext(ctx, `UPDATE jobs SET progress_message=? WHERE id=?`, msg, jobID)
	return err
}

// FailOrphanedJobs marks jobs left in queued/running after a process crash/restart.
func (d *DB) FailOrphanedJobs(ctx context.Context, reason string) (int64, error) {
	now := time.Now().UTC()
	res, err := d.SQL.ExecContext(ctx, `
		UPDATE jobs
		SET status='error',
		    error_message=?,
		    progress_message=?,
		    finished_at=?
		WHERE status IN ('queued', 'running')`,
		reason, reason, now,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (d *DB) ListJobs(ctx context.Context, tenantID string, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT id, tenant_id, COALESCE(schedule_id,''), service, job_type, status, started_at, finished_at,
			items_new, items_total, bytes_transferred, COALESCE(error_message,''), COALESCE(kopia_snapshot,''),
			progress_pct, COALESCE(progress_message,''), created_at
		FROM jobs WHERE tenant_id=? ORDER BY created_at DESC LIMIT ?`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (d *DB) GetJob(ctx context.Context, id string) (*Job, error) {
	row := d.SQL.QueryRowContext(ctx, `
		SELECT id, tenant_id, COALESCE(schedule_id,''), service, job_type, status, started_at, finished_at,
			items_new, items_total, bytes_transferred, COALESCE(error_message,''), COALESCE(kopia_snapshot,''),
			progress_pct, COALESCE(progress_message,''), created_at
		FROM jobs WHERE id=?`, id)
	var j Job
	var started, finished sql.NullTime
	if err := row.Scan(&j.ID, &j.TenantID, &j.ScheduleID, &j.Service, &j.JobType, &j.Status, &started, &finished,
		&j.ItemsNew, &j.ItemsTotal, &j.BytesTransferred, &j.ErrorMessage, &j.KopiaSnapshot,
		&j.ProgressPct, &j.ProgressMessage, &j.CreatedAt); err != nil {
		return nil, err
	}
	if started.Valid {
		j.StartedAt = started.Time
	}
	if finished.Valid {
		j.FinishedAt = finished.Time
	}
	return &j, nil
}

func scanJobs(rows *sql.Rows) ([]Job, error) {
	var out []Job
	for rows.Next() {
		var j Job
		var started, finished sql.NullTime
		if err := rows.Scan(&j.ID, &j.TenantID, &j.ScheduleID, &j.Service, &j.JobType, &j.Status, &started, &finished,
			&j.ItemsNew, &j.ItemsTotal, &j.BytesTransferred, &j.ErrorMessage, &j.KopiaSnapshot,
			&j.ProgressPct, &j.ProgressMessage, &j.CreatedAt); err != nil {
			return nil, err
		}
		if started.Valid {
			j.StartedAt = started.Time
		}
		if finished.Valid {
			j.FinishedAt = finished.Time
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
