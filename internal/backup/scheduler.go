package backup

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/tenant"
)

type Scheduler struct {
	DB      *db.DB
	Runner  *Runner
	Usage   *UsageScanner
	Log     *slog.Logger
	cron    *cron.Cron
	mu      sync.Mutex
	entryIDs []cron.EntryID
}

func NewScheduler(database *db.DB, runner *Runner, log *slog.Logger) *Scheduler {
	return &Scheduler{
		DB:     database,
		Runner: runner,
		Log:    log,
		cron:   cron.New(),
	}
}

func (s *Scheduler) Start(ctx context.Context) error {
	s.cron.Start()
	if err := s.Reload(ctx); err != nil {
		return err
	}
	// Daily key expiry check at 08:00
	_, _ = s.cron.AddFunc("0 8 * * *", func() {
		tenant.CheckSecretExpiry(context.Background(), s.DB, s.Runner.Notifier, s.Log)
	})
	// Hourly disk-usage (du) scan into tenant_usage
	if s.Usage != nil {
		_, _ = s.cron.AddFunc("15 * * * *", func() {
			if s.Usage.RefreshAll(context.Background()) {
				s.Log.Info("usage scan scheduled")
			} else {
				s.Log.Info("usage scan skipped (already running)")
			}
		})
		go func() {
			time.Sleep(45 * time.Second)
			if s.Usage.RefreshAll(context.Background()) {
				s.Log.Info("usage scan startup")
			}
		}()
	}
	return nil
}

func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
}

func (s *Scheduler) Reload(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.entryIDs {
		s.cron.Remove(id)
	}
	s.entryIDs = nil

	schedules, err := s.DB.ListAllSchedules(ctx)
	if err != nil {
		return err
	}
	for _, sch := range schedules {
		sch := sch
		id, err := s.cron.AddFunc(sch.CronExpr, func() {
			cctx := context.Background()
			t, err := s.DB.GetTenant(cctx, sch.TenantID)
			if err != nil {
				s.Log.Warn("scheduler skip", "schedule", sch.ID, "reason", "tenant load failed", "err", err)
				return
			}
			if t.Status != "active" {
				s.Log.Info("scheduler skip", "tenant", t.Name, "service", sch.Service, "reason", "tenant not active", "status", t.Status)
				return
			}
			jobType := "delta"
			if sch.Service == "pst" {
				jobType = "export"
			}
			s.Log.Info("scheduler fire", "tenant", t.Name, "service", sch.Service, "cron", sch.CronExpr, "job_type", jobType)
			_, err = s.Runner.Enqueue(cctx, sch.TenantID, sch.Service, sch.ID, jobType)
			if err != nil {
				s.Log.Error("enqueue scheduled job", "tenant", t.Name, "service", sch.Service, "err", err)
				return
			}
			sch.LastRun = time.Now().UTC()
			_ = s.DB.UpdateSchedule(cctx, &sch)
		})
		if err != nil {
			s.Log.Error("invalid cron", "expr", sch.CronExpr, "tenant", sch.TenantID, "service", sch.Service, "err", err)
			continue
		}
		s.entryIDs = append(s.entryIDs, id)
	}
	s.Log.Info("scheduler reloaded", "jobs", len(s.entryIDs))
	return nil
}
