package db

import (
	"context"
	"time"

	"github.com/google/uuid"
)

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

func (d *DB) ListJobLogs(ctx context.Context, jobID string) ([]JobLog, error) {
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT id, job_id, level, message, created_at
		FROM job_logs WHERE job_id=? ORDER BY created_at ASC, id ASC`, jobID)
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
