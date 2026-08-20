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

	"github.com/gofrs/flock"
	"github.com/google/uuid"

	"github.com/rhw/m365backup/internal/catalog"
	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/graph"
	"github.com/rhw/m365backup/internal/notification"
	"github.com/rhw/m365backup/internal/storage"
	"github.com/rhw/m365backup/internal/tenant"
)

type Runner struct {
	DB                *db.DB
	Tenants           *tenant.Manager
	Registry          *Registry
	Store             *storage.Engine
	Notifier          *notification.Service
	StagingRoot       string
	MaxConcurrent     int
	MaxConcurrentFull int
	Log               *slog.Logger

	sem          chan struct{}
	fullSem      chan struct{}
	mu           sync.Mutex
	cancels      map[string]context.CancelFunc
	enqueueMu    sync.Mutex
	serviceGates sync.Map // tenantID\0service -> *sync.Mutex (serialize runs per tenant+service)
}

// ErrTenantBusy is returned when Enqueue is refused because this tenant already
// has a queued or running job for the same service, or a full sync is active
// (incrementals must not start while a full sync is writing the live tree).
var ErrTenantBusy = errors.New("service already has an active backup job")

// TryRunnerLock takes an exclusive process lock so a second instance cannot
// start and race the first (RecoverOrphans would otherwise free the DB lock).
func TryRunnerLock(path string) (*flock.Flock, error) {
	l := flock.New(path)
	ok, err := l.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("another m365-backup instance already holds %s", path)
	}
	return l, nil
}

func NewRunner(database *db.DB, tenants *tenant.Manager, reg *Registry, store *storage.Engine, notifier *notification.Service, staging string, maxConc int, log *slog.Logger) *Runner {
	if maxConc < 1 {
		maxConc = 1
	}
	return &Runner{
		DB: database, Tenants: tenants, Registry: reg, Store: store, Notifier: notifier,
		StagingRoot: staging, MaxConcurrent: maxConc, MaxConcurrentFull: 1, Log: log,
		sem: make(chan struct{}, maxConc), fullSem: make(chan struct{}, 1),
		cancels: map[string]context.CancelFunc{},
	}
}

// SetMaxConcurrentFull caps how many job_type=full runs execute at once (default 1).
func (r *Runner) SetMaxConcurrentFull(n int) {
	if n < 1 {
		n = 1
	}
	r.MaxConcurrentFull = n
	r.fullSem = make(chan struct{}, n)
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

// PurgeStaging removes leftover job folders under StagingRoot.
// Only UUID-named directories are deleted so a store/ tree can share the same volume.
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
		if _, err := uuid.Parse(e.Name()); err != nil {
			continue
		}
		path, err := storage.EnsureSubpath(r.StagingRoot, e.Name())
		if err != nil {
			continue
		}
		path, err = storage.GuardPath(path)
		if err != nil {
			continue
		}
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

func (r *Runner) stagingJobDir(jobID string) (string, error) {
	if r.StagingRoot == "" {
		return "", fmt.Errorf("invalid path")
	}
	if _, err := storage.GuardPath(jobID); err != nil {
		return "", err
	}
	return storage.EnsureSubpath(r.StagingRoot, jobID)
}

func (r *Runner) cleanStagingJob(jobID string) {
	path, err := r.stagingJobDir(jobID)
	if err != nil {
		return
	}
	path, err = storage.GuardPath(path)
	if err != nil {
		return
	}
	_ = os.RemoveAll(path)
}

func (r *Runner) bindTenantStore(ctx context.Context, t *db.Tenant) error {
	if r.Tenants == nil || t == nil {
		return nil
	}
	old, err := r.Tenants.BindStorePath(ctx, t)
	if err != nil {
		return err
	}
	if r.Log != nil && old != "" && filepath.Clean(old) != filepath.Clean(t.StorePath) {
		r.Log.Info("rebased tenant store onto STORE_ROOT", "tenant", t.ID, "from", old, "to", t.StorePath)
	}
	return nil
}

func (r *Runner) Enqueue(ctx context.Context, tenantID, service, scheduleID, jobType string) (*db.Job, error) {
	return r.EnqueueParams(ctx, tenantID, service, scheduleID, jobType, "")
}

// EnqueueParams queues a job with optional JSON params (e.g. PST export scope).
func (r *Runner) EnqueueParams(ctx context.Context, tenantID, service, scheduleID, jobType, params string) (*db.Job, error) {
	// Serialize enqueue checks per process so two cron fires cannot both pass CountActiveJobs.
	r.enqueueMu.Lock()
	defer r.enqueueMu.Unlock()

	job, extras, err := r.enqueueLocked(ctx, tenantID, service, scheduleID, jobType, params, true)
	if err != nil {
		return nil, err
	}
	for _, svc := range extras {
		_, _, ferr := r.enqueueLocked(ctx, tenantID, svc, "", "full", "", false)
		if ferr == nil {
			continue
		}
		if errors.Is(ferr, ErrTenantBusy) {
			r.Log.Info("empty-store fan-out skipped (busy)", "tenant", tenantID, "service", svc)
			continue
		}
		r.Log.Error("empty-store fan-out", "tenant", tenantID, "service", svc, "err", ferr)
	}
	return job, nil
}

func (r *Runner) enqueueLocked(ctx context.Context, tenantID, service, scheduleID, jobType, params string, promoteEmpty bool) (*db.Job, []string, error) {
	var extras []string
	if promoteEmpty && isGraphBackupService(service) && jobType != "export" {
		svcEmpty, tenantEmpty, err := r.emptyStoreState(ctx, tenantID, service)
		if err != nil {
			return nil, nil, err
		}
		if jobType != "full" && svcEmpty {
			r.Log.Info("empty store — promoting to full Graph sync", "tenant", tenantID, "service", service, "was", jobType)
			jobType = "full"
		}
		if tenantEmpty {
			extras, err = r.enabledGraphServices(ctx, tenantID, service)
			if err != nil {
				return nil, nil, err
			}
			if len(extras) > 0 {
				r.Log.Info("empty tenant store — full-sync all enabled services", "tenant", tenantID, "trigger", service, "also", extras)
			}
		}
	}

	n, err := r.DB.CountActiveJobs(ctx, tenantID, service)
	if err != nil {
		return nil, nil, err
	}
	if n > 0 {
		r.Log.Info("enqueue skipped (service busy)", "tenant", tenantID, "service", service, "active", n)
		return nil, nil, ErrTenantBusy
	}

	// A full sync rewrites catalog / delta tokens. Cron incrementals
	// (any service) must wait — otherwise they snapshot a half-written tree.
	if jobType != "full" {
		fullN, err := r.DB.CountActiveFullJobs(ctx, tenantID)
		if err != nil {
			return nil, nil, err
		}
		if fullN > 0 {
			r.Log.Info("enqueue skipped (full sync active)", "tenant", tenantID, "service", service, "type", jobType, "full", fullN)
			return nil, nil, ErrTenantBusy
		}
	}

	if jobType == "full" {
		if err := r.DB.DeleteDeltaTokens(ctx, tenantID, service); err != nil {
			return nil, nil, err
		}
	}

	job := &db.Job{
		TenantID:   tenantID,
		ScheduleID: scheduleID,
		Service:    service,
		JobType:    jobType,
		Status:     "queued",
		Params:     params,
	}
	if err := r.DB.CreateJob(ctx, job); err != nil {
		if db.IsUniqueViolation(err) {
			r.Log.Info("enqueue skipped (unique active-job lock)", "tenant", tenantID, "service", service)
			return nil, nil, ErrTenantBusy
		}
		return nil, nil, err
	}
	r.Log.Info("job queued", "id", job.ID, "tenant", tenantID, "service", service, "type", jobType)
	go r.runJob(job.ID)
	return job, extras, nil
}

func isGraphBackupService(service string) bool {
	for _, s := range catalog.GraphBackupServices {
		if s == service {
			return true
		}
	}
	return false
}

func (r *Runner) emptyStoreState(ctx context.Context, tenantID, service string) (serviceEmpty, tenantEmpty bool, err error) {
	t, err := r.DB.GetTenant(ctx, tenantID)
	if err != nil {
		return false, false, err
	}
	if err := r.bindTenantStore(ctx, t); err != nil {
		return false, false, err
	}
	return catalog.EmptyLocalData(ctx, r.DB, tenantID, t.StorePath, service)
}

func (r *Runner) enabledGraphServices(ctx context.Context, tenantID, except string) ([]string, error) {
	schedules, err := r.DB.ListSchedules(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, sch := range schedules {
		if !sch.Enabled || sch.Service == except || !isGraphBackupService(sch.Service) {
			continue
		}
		out = append(out, sch.Service)
	}
	return out, nil
}

func tryServiceFileLock(storePath, service string) (*flock.Flock, error) {
	if storePath == "" || service == "" {
		return nil, fmt.Errorf("service lock: store path and service required")
	}
	if _, err := storage.GuardPath(service); err != nil {
		return nil, fmt.Errorf("service lock: %w", err)
	}
	lockDir, err := storage.EnsureSubpath(storePath, ".locks")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(lockDir, service+".lock")
	l := flock.New(lockPath)
	ok, err := l.TryLock()
	if err != nil {
		return nil, fmt.Errorf("service lock: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("service %s is already locked by another process", service)
	}
	return l, nil
}

func (r *Runner) serviceGate(tenantID, service string) *sync.Mutex {
	key := tenantID + "\x00" + service
	v, _ := r.serviceGates.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
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

// KillStale aborts a running job that stopped making progress (watchdog).
func (r *Runner) KillStale(ctx context.Context, jobID, reason string) error {
	job, err := r.DB.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Status != "running" {
		return nil
	}

	r.mu.Lock()
	if cancel, ok := r.cancels[jobID]; ok {
		cancel()
	}
	r.mu.Unlock()

	job.Status = "error"
	job.ErrorMessage = reason
	job.FinishedAt = time.Now().UTC()
	job.ProgressMessage = "Stalled — " + reason
	_ = r.DB.UpdateJob(ctx, job)
	_ = r.DB.InsertJobLog(ctx, &db.JobLog{JobID: job.ID, Level: "error", Message: reason})
	r.cleanStagingJob(jobID)
	if r.Notifier != nil {
		_ = r.Notifier.Send(ctx, notification.Event{
			Type: notification.EventJobError, TenantID: job.TenantID,
			Subject: "Backup stalled: " + notification.SafeService(job.Service),
			Body:    reason,
		})
	}
	r.Log.Warn("job killed (watchdog)", "id", jobID, "reason", reason)
	return nil
}

func (r *Runner) runJob(jobID string) {
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

	if job.JobType == "full" && r.fullSem != nil {
		r.Log.Info("waiting for full-sync slot", "id", jobID, "service", job.Service, "limit", r.MaxConcurrentFull)
		r.fullSem <- struct{}{}
		defer func() { <-r.fullSem }()
	}
	r.sem <- struct{}{}
	defer func() { <-r.sem }()

	job, err = r.DB.GetJob(base, jobID)
	if err != nil {
		r.Log.Error("load job", "id", jobID, "err", err)
		return
	}
	if job.Status == "cancelled" || job.Status == "error" {
		r.Log.Info("job skipped (already closed)", "id", jobID, "status", job.Status)
		return
	}

	// Exclusive per-tenant+service run lock (belt-and-suspenders vs. enqueue check).
	gate := r.serviceGate(job.TenantID, job.Service)
	gate.Lock()
	defer gate.Unlock()

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
	if err := r.bindTenantStore(ctx, t); err != nil {
		r.fail(ctx, job, err)
		return
	}
	svcLock, err := tryServiceFileLock(t.StorePath, job.Service)
	if err != nil {
		r.fail(ctx, job, err)
		return
	}
	defer func() { _ = svcLock.Unlock() }()
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

	clientSecret, storePass, err := r.Tenants.DecryptSecrets(t)
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

	cat, err := catalog.Open(r.DB, t.ID, t.StorePath, storePass)
	if err != nil {
		r.fail(ctx, job, err)
		return
	}
	if job.JobType == "full" {
		cat.SkipRematch = true
		cat.TrackChanges = false
	}
	imported, needFull, err := cat.EnsureMigrated(ctx, job.Service, job.ID)
	if err != nil {
		r.fail(ctx, job, err)
		return
	}
	if imported {
		prog.Emit("info", "imported existing sync/ tree as catalog generation 1")
	}
	if needFull && job.Service != "pst" {
		if job.JobType != "full" {
			if err := r.DB.DeleteDeltaTokens(ctx, t.ID, job.Service); err != nil {
				r.fail(ctx, job, err)
				return
			}
			job.JobType = "full"
			_ = r.DB.UpdateJob(ctx, job)
		}
		prog.Emit("info", "empty catalog — Graph full sync (delta tokens cleared)")
	}

	stageDir, err := r.stagingJobDir(job.ID)
	if err != nil {
		r.fail(ctx, job, err)
		return
	}
	stageDir, err = storage.GuardPath(stageDir)
	if err != nil {
		r.fail(ctx, job, err)
		return
	}
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
	result, runErr := svc.Run(ctx, gc, t, job, stageDir, r.DB, cat)
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
		prog.Emit("info", "skipping catalog snapshot (export job)")
		if result.ExportPath != "" {
			job.SnapshotID = filepath.Base(result.ExportPath)
		}
		_ = storage.ApplyPSTExportRetention(t.StorePath, policy.PSTKeepRuns)
		job.FinishedAt = time.Now().UTC()
		job.ProgressPct = 100
		job.ProgressMessage = summarizeResult(result)
		if result.ExportPath != "" {
			job.ProgressMessage = fmt.Sprintf("export %s · %s", job.SnapshotID, summarizeResult(result))
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
		r.Log.Info("job finished", "id", job.ID, "status", job.Status, "export", job.SnapshotID,
			"items", result.ItemsNew, "warnings", len(result.Warnings))
		return
	}

	prog.Emit("info", fmt.Sprintf("committing catalog snapshot (%d items, %d bytes)…", result.ItemsNew, result.BytesTransferred))
	job.ProgressPct = 95
	job.ProgressMessage = "Committing catalog snapshot…"
	_ = r.DB.UpdateJobProgress(ctx, job)
	snap, err := cat.CommitSnapshotWithProgress(ctx, job.Service, job.ID, func(done, total int, msg string) {
		if msg == "" {
			return
		}
		job.ProgressMessage = msg
		if total > 0 && done > 0 {
			// Keep bar in the 95–99% band while the manifest streams.
			pct := 95 + (done*4)/total
			if pct > 99 {
				pct = 99
			}
			job.ProgressPct = pct
		}
		_ = r.DB.UpdateJobProgress(context.Background(), job)
		prog.Emit("info", msg)
	})
	if err != nil {
		if isCancelErr(err) || r.wasCancelled(job.ID) {
			r.finishCancelled(ctx, job, "cancelled by user")
			return
		}
		r.fail(ctx, job, fmt.Errorf("snapshot: %w", err))
		return
	}
	catalog.RemoveLegacyDirs(t.StorePath)
	if snap == nil || snap.Skipped {
		skipMsg := "no catalog changes — snapshot skipped"
		if snap != nil {
			skipMsg = fmt.Sprintf("no catalog changes — snapshot skipped (generation %d)", snap.Generation)
			job.SnapshotID = snap.ID
		}
		prog.Emit("info", skipMsg)
		if r.wasCancelled(job.ID) {
			r.finishCancelled(ctx, job, "cancelled by user")
			return
		}
		job.ItemsNew = result.ItemsNew
		job.ItemsTotal = result.ItemsTotal
		job.BytesTransferred = result.BytesTransferred
		job.FinishedAt = time.Now().UTC()
		job.ProgressPct = 100
		job.ProgressMessage = skipMsg
		if len(result.Warnings) > 0 {
			job.Status = "warning"
			job.ErrorMessage = summarizeResult(result)
		} else {
			job.Status = "success"
			job.ErrorMessage = summarizeResult(result)
		}
		if !result.livePersisted {
			_ = r.persistLogs(ctx, job.ID, result.Logs)
		}
		_ = r.DB.UpdateJob(ctx, job)
		r.Log.Info("job finished", "id", job.ID, "status", job.Status, "snapshot", "skipped",
			"items", result.ItemsNew, "skipped", result.SkippedUsers, "warnings", len(result.Warnings))
		return
	}
	if n, err := cat.ApplySmartRetention(ctx, policy); err != nil {
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
	job.SnapshotID = snap.ID
	job.FinishedAt = time.Now().UTC()
	job.ProgressPct = 100

	snapMsg := fmt.Sprintf("snapshot %s stored (generation %d)", snap.ID, snap.Generation)
	if len(result.Logs) == 0 {
		snapMsg = fmt.Sprintf("snapshot %s created (%d items)", snap.ID, result.ItemsNew)
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
		if r.Notifier != nil {
			_ = r.Notifier.Send(ctx, notification.Event{
				Type: notification.EventJobWarning, TenantID: t.ID,
				Subject: "Backup warning: " + t.Name + " / " + notification.SafeService(job.Service),
				Body:    job.ErrorMessage + "\n\nSee job detail log for full list.",
			})
		}
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
	if r.Notifier != nil {
		_ = r.Notifier.Send(context.Background(), notification.Event{
			Type: notification.EventJobError, TenantID: job.TenantID,
			Subject: "Backup failed: " + notification.SafeService(job.Service),
			Body:    err.Error(),
		})
	}
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
