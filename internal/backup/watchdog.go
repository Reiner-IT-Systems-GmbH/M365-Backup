package backup

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rhw/m365backup/internal/db"
	"log/slog"
)

// Watchdog kills running jobs that show no progress for JobStallTimeout.
type Watchdog struct {
	Runner *Runner
	DB     *db.DB
	Log    *slog.Logger

	Interval time.Duration
	Stall    time.Duration

	mu    sync.Mutex
	track map[string]stallTrack
}

type stallTrack struct {
	bytes    int64
	items    int
	pct      int
	message  string
	lastMove time.Time
}

func NewWatchdog(r *Runner, database *db.DB, interval, stall time.Duration) *Watchdog {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if stall <= 0 {
		stall = 2 * time.Hour
	}
	return &Watchdog{
		Runner:   r,
		DB:       database,
		Interval: interval,
		Stall:    stall,
		track:    map[string]stallTrack{},
	}
}

func (w *Watchdog) Start(ctx context.Context) {
	if w == nil || w.Runner == nil || w.DB == nil || w.Stall <= 0 {
		return
	}
	go w.loop(ctx)
}

func (w *Watchdog) loop(ctx context.Context) {
	tick := time.NewTicker(w.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			w.check(context.Background())
		}
	}
}

func (w *Watchdog) check(ctx context.Context) {
	jobs, err := w.DB.ListRunningJobs(ctx)
	if err != nil {
		if w.Log != nil {
			w.Log.Warn("watchdog list jobs", "err", err)
		}
		return
	}
	seen := map[string]struct{}{}
	now := time.Now().UTC()
	for i := range jobs {
		j := &jobs[i]
		seen[j.ID] = struct{}{}
		if w.evaluate(ctx, j, now) {
			reason := fmt.Sprintf("no progress for %s (watchdog)", w.Stall.Round(time.Minute))
			if err := w.Runner.KillStale(ctx, j.ID, reason); err != nil && w.Log != nil {
				w.Log.Warn("watchdog kill", "job", j.ID, "err", err)
			}
		}
	}
	w.prune(seen)
}

func (w *Watchdog) evaluate(ctx context.Context, j *db.Job, now time.Time) (kill bool) {
	cur := stallTrack{
		bytes:   j.BytesTransferred,
		items:   j.ItemsNew,
		pct:     j.ProgressPct,
		message: j.ProgressMessage,
	}

	w.mu.Lock()
	prev, ok := w.track[j.ID]
	if !ok {
		cur.lastMove = w.initialActivity(ctx, j, now)
		w.track[j.ID] = cur
		w.mu.Unlock()
		return now.Sub(cur.lastMove) >= w.Stall
	}
	if prev.bytes != cur.bytes || prev.items != cur.items || prev.pct != cur.pct || prev.message != cur.message {
		cur.lastMove = now
	} else {
		cur.lastMove = prev.lastMove
	}
	w.track[j.ID] = cur
	w.mu.Unlock()

	if now.Sub(cur.lastMove) >= w.Stall {
		return true
	}
	return false
}

func (w *Watchdog) initialActivity(ctx context.Context, j *db.Job, now time.Time) time.Time {
	if j.BytesTransferred > 0 || j.ItemsNew > 0 || j.ProgressPct > 0 {
		if logAt, err := w.DB.LastJobLogTime(ctx, j.ID); err == nil && !logAt.IsZero() {
			return logAt
		}
		return now
	}
	t := now
	if !j.StartedAt.IsZero() {
		t = j.StartedAt
	}
	if logAt, err := w.DB.LastJobLogTime(ctx, j.ID); err == nil && !logAt.IsZero() && logAt.After(t) {
		t = logAt
	}
	return t
}

func (w *Watchdog) prune(seen map[string]struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id := range w.track {
		if _, ok := seen[id]; !ok {
			delete(w.track, id)
		}
	}
}
