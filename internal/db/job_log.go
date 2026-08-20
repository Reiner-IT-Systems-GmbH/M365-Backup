package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

const DefaultJobLogTail = 500

type JobLog struct {
	ID        string
	JobID     string
	Level     string // info | warn | error | skip
	Message   string
	CreatedAt time.Time
}

func (d *DB) InsertJobLog(ctx context.Context, l *JobLog) error {
	if l.ID == "" {
		l.ID = uuid.NewString()
	}
	if l.CreatedAt.IsZero() {
		l.CreatedAt = time.Now().UTC()
	}
	_, err := d.SQL.ExecContext(ctx, `
		INSERT INTO job_logs (id, job_id, level, message, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		l.ID, l.JobID, l.Level, l.Message, l.CreatedAt,
	)
	return err
}

func (d *DB) InsertJobLogs(ctx context.Context, jobID string, logs []JobLog) error {
	for i := range logs {
		logs[i].JobID = jobID
		if err := d.InsertJobLog(ctx, &logs[i]); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) CountJobLogs(ctx context.Context, jobID string) (int, error) {
	var n int
	err := d.SQL.QueryRowContext(ctx, `SELECT COUNT(1) FROM job_logs WHERE job_id=?`, jobID).Scan(&n)
	return n, err
}

func (d *DB) ListJobLogs(ctx context.Context, jobID string) ([]JobLog, error) {
	return d.listJobLogsQuery(ctx, `
		SELECT id, job_id, level, message, created_at
		FROM job_logs WHERE job_id=? ORDER BY created_at ASC, id ASC`, jobID)
}

// ListJobLogsTail returns the newest up-to-limit lines in chronological order.
func (d *DB) ListJobLogsTail(ctx context.Context, jobID string, limit int) ([]JobLog, int, error) {
	if limit <= 0 {
		limit = DefaultJobLogTail
	}
	total, err := d.CountJobLogs(ctx, jobID)
	if err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}
	if total <= limit {
		logs, err := d.ListJobLogs(ctx, jobID)
		return logs, total, err
	}
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT id, job_id, level, message, created_at
		FROM job_logs WHERE job_id=?
		ORDER BY created_at DESC, id DESC
		LIMIT ?`, jobID, limit)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	var tail []JobLog
	for rows.Next() {
		var l JobLog
		if err := rows.Scan(&l.ID, &l.JobID, &l.Level, &l.Message, &l.CreatedAt); err != nil {
			return nil, total, err
		}
		tail = append(tail, l)
	}
	if err := rows.Err(); err != nil {
		return nil, total, err
	}
	for i, j := 0, len(tail)-1; i < j; i, j = i+1, j-1 {
		tail[i], tail[j] = tail[j], tail[i]
	}
	return tail, total, nil
}

// ListJobLogsAfter returns log lines newer than the given cursor (exclusive).
func (d *DB) ListJobLogsAfter(ctx context.Context, jobID string, afterCreated time.Time, afterID string, limit int) ([]JobLog, error) {
	if limit <= 0 {
		limit = DefaultJobLogTail
	}
	if afterID == "" && afterCreated.IsZero() {
		return nil, nil
	}
	return d.listJobLogsQuery(ctx, `
		SELECT id, job_id, level, message, created_at
		FROM job_logs
		WHERE job_id=? AND (created_at > ? OR (created_at = ? AND id > ?))
		ORDER BY created_at ASC, id ASC
		LIMIT ?`, jobID, afterCreated, afterCreated, afterID, limit)
}

func (d *DB) listJobLogsQuery(ctx context.Context, q string, args ...any) ([]JobLog, error) {
	rows, err := d.SQL.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JobLog
	for rows.Next() {
		var l JobLog
		if err := rows.Scan(&l.ID, &l.JobID, &l.Level, &l.Message, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LastJobLogTime returns the newest log line timestamp for a job, or zero if none.
func (d *DB) LastJobLogTime(ctx context.Context, jobID string) (time.Time, error) {
	var t sql.NullTime
	err := d.SQL.QueryRowContext(ctx, `
		SELECT MAX(created_at) FROM job_logs WHERE job_id=?`, jobID).Scan(&t)
	if err != nil {
		return time.Time{}, err
	}
	if !t.Valid {
		return time.Time{}, nil
	}
	return t.Time, nil
}
