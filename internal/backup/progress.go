package backup

import (
	"context"
	"log/slog"

	"github.com/rhw/m365backup/internal/db"
)

type progressKey struct{}

// Progress streams job activity to stdout (docker logs) and job_logs (UI) in real time.
type Progress struct {
	JobID   string
	Tenant  string
	Service string
	DB      *db.DB
	Log     *slog.Logger
}

func WithProgress(ctx context.Context, p *Progress) context.Context {
	return context.WithValue(ctx, progressKey{}, p)
}

func ProgressFrom(ctx context.Context) *Progress {
	p, _ := ctx.Value(progressKey{}).(*Progress)
	return p
}

func clampPct(pct int) int {
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

func (p *Progress) setMessage(msg string) {
	if p == nil || p.DB == nil || p.JobID == "" || msg == "" {
		return
	}
	_ = p.DB.UpdateJobProgressMessage(context.Background(), p.JobID, msg)
}

func (p *Progress) Emit(level, msg string) {
	if p == nil {
		return
	}
	attrs := []any{"job", p.JobID, "tenant", p.Tenant, "service", p.Service}
	switch level {
	case "warn":
		p.Log.Warn(msg, attrs...)
	case "error":
		p.Log.Error(msg, attrs...)
	default:
		p.Log.Info(msg, attrs...)
	}
	if p.DB != nil && p.JobID != "" {
		_ = p.DB.InsertJobLog(context.Background(), &db.JobLog{
			JobID:   p.JobID,
			Level:   level,
			Message: msg,
		})
		if level == "info" || level == "warn" {
			p.setMessage(msg)
		}
	}
}

// SyncJob writes counters + percent so the UI progress bar moves.
func (p *Progress) SyncJob(job *db.Job, res *Result, pct int, msg string) {
	if p == nil || p.DB == nil || job == nil || res == nil {
		return
	}
	itemsNew, itemsTotal, _, bytes := res.snapshot()
	job.ItemsNew = itemsNew
	job.ItemsTotal = itemsTotal
	job.BytesTransferred = bytes
	job.ProgressPct = clampPct(pct)
	if msg != "" {
		job.ProgressMessage = msg
	}
	_ = p.DB.UpdateJobProgress(context.Background(), job)
}

// NewResult returns a Result that mirrors Info/Warn/Error to Progress (if present on ctx).
func NewResult(ctx context.Context) Result {
	return Result{progress: ProgressFrom(ctx)}
}
