package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/graph"
	"github.com/rhw/m365backup/internal/notification"
	"github.com/rhw/m365backup/internal/storage"
	"github.com/rhw/m365backup/internal/tenant"
)

type Runner struct {
	DB            *db.DB
	Tenants       *tenant.Manager
	Registry      *Registry
	Store         *storage.Engine
	Notifier      *notification.Service
	StagingRoot   string
	MaxConcurrent int
	Log           *slog.Logger

	sem     chan struct{}
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func NewRunner(database *db.DB, tenants *tenant.Manager, reg *Registry, store *storage.Engine, notifier *notification.Service, staging string, maxConc int, log *slog.Logger) *Runner {
	if maxConc < 1 {
		maxConc = 1
	}
	return &Runner{
		DB: database, Tenants: tenants, Registry: reg, Store: store, Notifier: notifier,
		StagingRoot: staging, MaxConcurrent: maxConc, Log: log,
		sem: make(chan struct{}, maxConc), cancels: map[string]context.CancelFunc{},
	}
}

// RecoverOrphans marks queued/running jobs left behind by a previous process as failed
// and wipes leftover staging dirs from crashed runs.
func (r *Runner) RecoverOrphans(ctx context.Context) {
	n, err := r.DB.FailOrphanedJobs(ctx, "interrupted by process restart")
	if err != nil {
		r.Log.Error("recover orphaned jobs", "err", err)
	} else if n > 0 {
		r.Log.Warn("marked orphaned jobs as error", "count", n)
	}
	r.PurgeStaging()
}

// PurgeStaging removes all leftover job folders under StagingRoot.
// Safe at process start (no live jobs yet) and after crashes.
func (r *Runner) PurgeStaging() {
	if r.StagingRoot == "" {
		return
	}
	entries, err := os.ReadDir(r.StagingRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			r.Log.Warn("purge staging: read", "err", err)
		}
		return
	}
	removed := 0
	for _, e := range entries {
		path := filepath.Join(r.StagingRoot, e.Name())
		if err := os.RemoveAll(path); err != nil {
			r.Log.Warn("purge staging: remove", "path", path, "err", err)
			continue
		}
		removed++
	}
	if removed > 0 {
		r.Log.Info("purged leftover staging", "entries", removed)
	}
}

func (r *Runner) cleanStagingJob(jobID string) {
	if r.StagingRoot == "" || jobID == "" {
		return
	}
	_ = os.RemoveAll(filepath.Join(r.StagingRoot, jobID))
}

func (r *Runner) Enqueue(ctx context.Context, tenantID, service, scheduleID, jobType string) (*db.Job, error) {
	job := &db.Job{
		TenantID:   tenantID,
		ScheduleID: scheduleID,
		Service:    service,
		JobType:    jobType,
		Status:     "queued",
	}
	if err := r.DB.CreateJob(ctx, job); err != nil {
		return nil, err
	}
	r.Log.Info("job queued", "id", job.ID, "tenant", tenantID, "service", service, "type", jobType)
	go r.runJob(job.ID)
	return job, nil
}

// Cancel stops a queued or running job. Safe to call from the UI.
func (r *Runner) Cancel(ctx context.Context, jobID string) error {
	job, err := r.DB.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Status != "queued" && job.Status != "running" {
		return fmt.Errorf("job is not active (status=%s)", job.Status)
	}

	r.mu.Lock()
	if cancel, ok := r.cancels[jobID]; ok {
		cancel()
	}
	r.mu.Unlock()

	job.Status = "cancelled"
	job.FinishedAt = time.Now().UTC()
	job.ErrorMessage = "cancelled by user"
	job.ProgressMessage = "Cancelled"
	_ = r.DB.UpdateJob(ctx, job)
	_ = r.DB.InsertJobLog(ctx, &db.JobLog{JobID: job.ID, Level: "warn", Message: "cancelled by user"})
	r.cleanStagingJob(jobID)
	r.Log.Info("job cancelled", "id", jobID)
	return nil
}

func (r *Runner) runJob(jobID string) {
	r.sem <- struct{}{}
	defer func() { <-r.sem }()

	base := context.Background()
	job, err := r.DB.GetJob(base, jobID)
	if err != nil {
		r.Log.Error("load job", "id", jobID, "err", err)
		return
	}
	if job.Status == "cancelled" || job.Status == "error" {
		r.Log.Info("job skipped (already closed)", "id", jobID, "status", job.Status)
		return
	}

	ctx, cancel := context.WithCancel(base)
	r.mu.Lock()
	r.cancels[jobID] = cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.cancels, jobID)
		r.mu.Unlock()
		cancel()
	}()

	job.Status = "running"
	job.StartedAt = time.Now().UTC()
	_ = r.DB.UpdateJob(ctx, job)

	t, err := r.DB.GetTenant(ctx, job.TenantID)
	if err != nil {
		r.fail(ctx, job, err)
		return
	}
	svc, ok := r.Registry.Get(job.Service)
	if !ok {
		r.fail(ctx, job, fmt.Errorf("unknown service %s", job.Service))
		return
	}

	prog := &Progress{
		JobID: job.ID, Tenant: t.Name, Service: job.Service,
		DB: r.DB, Log: r.Log,
	}
	ctx = WithProgress(ctx, prog)
	prog.Emit("info", fmt.Sprintf("job started (%s / %s)", t.Name, job.Service))
	job.ProgressPct = 1
	job.ProgressMessage = "Starting…"
	_ = r.DB.UpdateJobProgress(ctx, job)

	clientSecret, kopiaPass, err := r.Tenants.DecryptSecrets(t)
	if err != nil {
		r.fail(ctx, job, err)
		return
	}
	var gc *graph.Client
	if job.Service != "pst" {
		gc, err = graph.New(ctx, t.AzureTenantID, t.ClientID, clientSecret)
		if err != nil {
			r.fail(ctx, job, err)
			return
		}
	} else {
		_ = clientSecret
	}

	stageDir := filepath.Join(r.StagingRoot, job.ID)
	_ = os.RemoveAll(stageDir)
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		r.fail(ctx, job, err)
		return
	}
	defer func() { _ = os.RemoveAll(stageDir) }()

	prog.Emit("info", "running service backup…")
	job.ProgressPct = 2
	job.ProgressMessage = "Running service backup…"
	_ = r.DB.UpdateJobProgress(ctx, job)
	result, runErr := svc.Run(ctx, gc, t, job, stageDir, r.DB)
	if runErr != nil {
		if isCancelErr(runErr) || r.wasCancelled(job.ID) {
			r.finishCancelled(ctx, job, "cancelled by user")
			return
		}
		r.fail(ctx, job, runErr)
		return
	}
	if r.wasCancelled(job.ID) || ctx.Err() != nil {
		r.finishCancelled(ctx, job, "cancelled by user")
		return
	}

	job.ItemsNew = result.ItemsNew
	job.ItemsTotal = result.ItemsTotal
	job.BytesTransferred = result.BytesTransferred

	policy := storage.ParseRetentionJSON(t.RetentionJSON)

	if result.SkipSnapshot {
		prog.Emit("info", "skipping encrypted snapshot (export job)")
		if result.ExportPath != "" {
			job.KopiaSnapshot = filepath.Base(result.ExportPath)
		}
		_ = storage.ApplyPSTExportRetention(t.KopiaRepoPath, policy.PSTKeepRuns)
		job.FinishedAt = time.Now().UTC()
		job.ProgressPct = 100
		job.ProgressMessage = summarizeResult(result)
		if result.ExportPath != "" {
			job.ProgressMessage = fmt.Sprintf("export %s · %s", job.KopiaSnapshot, summarizeResult(result))
		}
		if !result.livePersisted {
			_ = r.persistLogs(ctx, job.ID, result.Logs)
		}
		if len(result.Warnings) > 0 {
			job.Status = "warning"
			job.ErrorMessage = summarizeResult(result)
		} else {
			job.Status = "success"
			job.ErrorMessage = summarizeResult(result)
		}
		_ = r.DB.UpdateJob(ctx, job)
		r.Log.Info("job finished", "id", job.ID, "status", job.Status, "export", job.KopiaSnapshot,
			"items", result.ItemsNew, "warnings", len(result.Warnings))
		return
	}

	snapSource := stageDir
	if result.SnapshotDir != "" {
		snapSource = result.SnapshotDir
	}
	prog.Emit("info", fmt.Sprintf("creating snapshot from %s (%d items, %d bytes)…", snapSource, result.ItemsNew, result.BytesTransferred))
	job.ProgressPct = 95
	job.ProgressMessage = "Creating encrypted snapshot…"
	_ = r.DB.UpdateJobProgress(ctx, job)
	snap, err := r.Store.Snapshot(ctx, t.KopiaRepoPath, kopiaPass, snapSource, job.Service)
	if err != nil {
		if isCancelErr(err) || r.wasCancelled(job.ID) {
			r.finishCancelled(ctx, job, "cancelled by user")
			return
		}
		r.fail(ctx, job, fmt.Errorf("snapshot: %w", err))
		return
	}
	if n, err := r.Store.ApplySmartRetention(ctx, t.KopiaRepoPath, kopiaPass, policy); err != nil {
		prog.Emit("warn", fmt.Sprintf("retention: %v", err))
	} else if n > 0 {
		prog.Emit("info", fmt.Sprintf("Smart Recycle: %d alte Snapshots entfernt", n))
	}

	if r.wasCancelled(job.ID) {
		r.finishCancelled(ctx, job, "cancelled by user")
		return
	}

	job.ItemsNew = result.ItemsNew
	job.ItemsTotal = result.ItemsTotal
	job.BytesTransferred = result.BytesTransferred
	job.KopiaSnapshot = snap.ID
	job.FinishedAt = time.Now().UTC()
	job.ProgressPct = 100

	snapMsg := fmt.Sprintf("snapshot %s stored", snap.ID)
	if len(result.Logs) == 0 {
		snapMsg = fmt.Sprintf("snapshot %s created (%d files in staging)", snap.ID, result.ItemsNew)
	}
	job.ProgressMessage = snapMsg
	if result.livePersisted {
		prog.Emit("info", snapMsg)
	} else {
		result.Info(snapMsg)
		_ = r.persistLogs(ctx, job.ID, result.Logs)
	}

	if len(result.Warnings) > 0 {
		job.Status = "warning"
		job.ErrorMessage = summarizeResult(result)
		_ = r.Notifier.Send(ctx, notification.Event{
			Type: notification.EventJobWarning, TenantID: t.ID,
			Subject: "Backup warning: " + t.Name + " / " + job.Service,
			Body:    job.ErrorMessage + "\n\nSee job detail log for full list.",
		})
	} else {
		job.Status = "success"
		job.ErrorMessage = summarizeResult(result)
	}
	_ = r.DB.UpdateJob(ctx, job)
	r.Log.Info("job finished", "id", job.ID, "status", job.Status, "snapshot", snap.ID,
		"items", result.ItemsNew, "skipped", result.SkippedUsers, "warnings", len(result.Warnings))
}

func (r *Runner) wasCancelled(jobID string) bool {
	job, err := r.DB.GetJob(context.Background(), jobID)
	return err == nil && job.Status == "cancelled"
}

func isCancelErr(err error) bool {
	return err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(strings.ToLower(err.Error()), "context canceled"))
}

func (r *Runner) finishCancelled(ctx context.Context, job *db.Job, reason string) {
	defer r.cleanStagingJob(job.ID)
	// Preserve cancelled status if Cancel() already wrote it; otherwise set it now.
	fresh, err := r.DB.GetJob(context.Background(), job.ID)
	if err == nil && fresh.Status == "cancelled" {
		r.Log.Info("job cancelled", "id", job.ID)
		return
	}
	job.Status = "cancelled"
	job.ErrorMessage = reason
	job.FinishedAt = time.Now().UTC()
	job.ProgressMessage = "Cancelled"
	_ = r.DB.UpdateJob(context.Background(), job)
	_ = r.DB.InsertJobLog(context.Background(), &db.JobLog{JobID: job.ID, Level: "warn", Message: reason})
	r.Log.Info("job cancelled", "id", job.ID)
}

func (r *Runner) fail(ctx context.Context, job *db.Job, err error) {
	defer r.cleanStagingJob(job.ID)
	if r.wasCancelled(job.ID) || isCancelErr(err) {
		r.finishCancelled(ctx, job, "cancelled by user")
		return
	}
	job.Status = "error"
	job.ErrorMessage = err.Error()
	job.FinishedAt = time.Now().UTC()
	job.ProgressMessage = "Failed: " + err.Error()
	_ = r.DB.UpdateJob(context.Background(), job)
	_ = r.DB.InsertJobLog(context.Background(), &db.JobLog{JobID: job.ID, Level: "error", Message: err.Error()})
	r.Log.Error("job failed", "id", job.ID, "err", err)
	_ = r.Notifier.Send(context.Background(), notification.Event{
		Type: notification.EventJobError, TenantID: job.TenantID,
		Subject: "Backup failed: " + job.Service,
		Body:    err.Error(),
	})
}

func (r *Runner) persistLogs(ctx context.Context, jobID string, lines []LogLine) error {
	logs := make([]db.JobLog, 0, len(lines))
	for _, l := range lines {
		if l.Level == "skip" {
			continue // never store per-user skip spam
		}
		logs = append(logs, db.JobLog{Level: l.Level, Message: l.Message})
	}
	return r.DB.InsertJobLogs(ctx, jobID, logs)
}

func summarizeResult(r Result) string {
	if r.ExportPath != "" || r.SkipSnapshot {
		parts := []string{fmt.Sprintf("%d Postfächer exportiert", r.ItemsNew)}
		if r.BytesTransferred > 0 {
			parts = append(parts, storage.FormatBytes(r.BytesTransferred))
		}
		if len(r.Warnings) > 0 {
			parts = append(parts, fmt.Sprintf("%d warnings", len(r.Warnings)))
		}
		return strings.Join(parts, " · ")
	}
	parts := []string{
		fmt.Sprintf("%d items backed up", r.ItemsNew),
	}
	if r.ItemsTotal > 0 {
		parts = append(parts, fmt.Sprintf("%d users checked", r.ItemsTotal))
	}
	if r.SkippedUsers > 0 {
		parts = append(parts, fmt.Sprintf("%d without mailbox (ignored)", r.SkippedUsers))
	}
	if len(r.Warnings) > 0 {
		parts = append(parts, fmt.Sprintf("%d real warnings", len(r.Warnings)))
	}
	if r.BytesTransferred > 0 {
		parts = append(parts, fmt.Sprintf("%d bytes", r.BytesTransferred))
	}
	return strings.Join(parts, " · ")
}

func joinWarnings(w []string) string {
	out := ""
	for i, s := range w {
		if i > 0 {
			out += "; "
		}
		out += s
	}
	return out
}

// Ensure *db.DB implements TokenStore.
var _ TokenStore = (*db.DB)(nil)
