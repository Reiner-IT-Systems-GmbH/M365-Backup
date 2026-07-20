package backup

import (
	"context"
	"sync"

	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/graph"
)

type LogLine struct {
	Level   string // info | warn | error | skip
	Message string
}

type Result struct {
	ItemsNew         int
	ItemsTotal       int
	BytesTransferred int64
	SkippedUsers     int
	Warnings         []string
	Logs             []LogLine
	SnapshotDir      string // if set, runner snapshots this path instead of stageDir
	SkipSnapshot     bool   // if true, runner skips encrypted snapshot (e.g. PST export)
	ExportPath       string // artifact path for export jobs (shown in UI)
	progress         *Progress
	livePersisted    bool
	mu               sync.Mutex
}

func (r *Result) Info(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Logs = append(r.Logs, LogLine{Level: "info", Message: msg})
	if r.progress != nil {
		r.progress.Emit("info", msg)
		r.livePersisted = true
	}
}
func (r *Result) Warn(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Warnings = append(r.Warnings, msg)
	r.Logs = append(r.Logs, LogLine{Level: "warn", Message: msg})
	if r.progress != nil {
		r.progress.Emit("warn", msg)
		r.livePersisted = true
	}
}
func (r *Result) Skip(_ string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.SkippedUsers++
}
func (r *Result) Error(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Logs = append(r.Logs, LogLine{Level: "error", Message: msg})
	if r.progress != nil {
		r.progress.Emit("error", msg)
		r.livePersisted = true
	}
}

func (r *Result) addItems(n int, bytes int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ItemsNew += n
	r.BytesTransferred += bytes
}

func (r *Result) addTotal(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ItemsTotal += n
}

func (r *Result) snapshot() (itemsNew, itemsTotal, skipped int, bytes int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ItemsNew, r.ItemsTotal, r.SkippedUsers, r.BytesTransferred
}

// ServiceBackup is implemented by Exchange, OneDrive, Teams, SharePoint.
type ServiceBackup interface {
	Name() string
	Run(ctx context.Context, gc *graph.Client, tenant *db.Tenant, job *db.Job, stageDir string, tokens TokenStore) (Result, error)
}

type TokenStore interface {
	GetDeltaToken(ctx context.Context, tenantID, service, userID string) (string, error)
	UpsertDeltaToken(ctx context.Context, t db.DeltaToken) error
}

type Registry struct {
	services map[string]ServiceBackup
}

func NewRegistry(svcs ...ServiceBackup) *Registry {
	r := &Registry{services: map[string]ServiceBackup{}}
	for _, s := range svcs {
		r.services[s.Name()] = s
	}
	return r
}

func (r *Registry) Get(name string) (ServiceBackup, bool) {
	s, ok := r.services[name]
	return s, ok
}

func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.services))
	for k := range r.services {
		out = append(out, k)
	}
	return out
}