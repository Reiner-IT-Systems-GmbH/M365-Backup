package api

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/rhw/m365backup/internal/backup"
	"github.com/rhw/m365backup/internal/config"
	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/graph"
	"github.com/rhw/m365backup/internal/notification"
	"github.com/rhw/m365backup/internal/storage"
	"github.com/rhw/m365backup/internal/tenant"
)

type Server struct {
	Cfg       *config.Config
	DB        *db.DB
	Tenants   *tenant.Manager
	Runner    *backup.Runner
	Sched     *backup.Scheduler
	Store     *storage.Engine
	Notifier  *notification.Service
	Sessions  *SessionStore
	Usage     *backup.UsageScanner
	Templates *template.Template
	Static    fs.FS
	OpenAPI   []byte // embedded OpenAPI 3 YAML; servers rewritten per request
	Log       *slog.Logger
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)
	r.Use(s.Sessions.Middleware)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Get("/login", s.handleLoginForm)
	r.Post("/login", s.handleLogin)
	r.Post("/logout", s.handleLogout)

	r.Get("/", s.handleHome)
	r.Get("/tenants", s.handleTenants)
	r.Post("/tenants/usage/refresh", s.handleUsageRefreshAll)
	r.Get("/tenants/new", s.handleTenantNewForm)
	r.Post("/tenants", s.handleTenantCreate)
	r.Get("/tenants/{id}", s.handleTenantDetail)
	r.Post("/tenants/{id}/usage/refresh", s.handleUsageRefreshOne)
	r.Post("/tenants/{id}/backup/{service}", s.handleTriggerBackup)
	r.Post("/tenants/{id}/schedules/{sid}", s.handleUpdateSchedule)
	r.Post("/tenants/{id}/retention", s.handleUpdateRetention)
	r.Get("/tenants/{id}/recovery", s.handleRecoveryForm)
	r.Post("/tenants/{id}/recovery", s.handleRecoveryExport)
	r.Get("/tenants/{id}/exports/pst/{runID}/{file}", s.handlePSTExportDownload)
	r.Get("/tenants/{id}/restore", s.handleRestoreForm)
	r.Post("/tenants/{id}/restore", s.handleRestore)
	r.Get("/tenants/{id}/browser", s.handleBrowser)
	r.Get("/tenants/{id}/browser/file", s.handleBrowserFile)
	r.Get("/tenants/{id}/jobs", s.handleJobsPartial)
	r.Get("/tenants/{id}/jobs/{jobID}", s.handleJobDetail)
	r.Get("/tenants/{id}/jobs/{jobID}/live", s.handleJobLive)
	r.Post("/tenants/{id}/jobs/{jobID}/cancel", s.handleCancelJob)
	r.Get("/tenants/{id}/snapshots/{snapID}", s.handleSnapshotBrowse)
	r.Get("/tenants/{id}/snapshots/{snapID}/file", s.handleSnapshotFile)
	r.Get("/settings", s.handleSettings)
	r.Post("/settings/notifications", s.handleSaveNotifications)
	r.Get("/openapi", s.handleOpenAPIPage)
	r.Get("/openapi.yaml", s.handleOpenAPISpec)

	r.Get("/api/tenants", s.apiListTenants)
	r.Post("/api/tenants", s.apiCreateTenant)
	r.Get("/api/tenants/{id}/usage", s.apiTenantUsage)
	r.Post("/api/tenants/usage/refresh", s.apiUsageRefreshAll)
	r.Post("/api/tenants/{id}/usage/refresh", s.apiUsageRefreshOne)
	r.Get("/api/tenants/{id}/jobs", s.apiListJobs)
	r.Put("/api/tenants/{id}/schedule", s.apiUpdateSchedules)
	r.Post("/api/tenants/{id}/restore", s.apiRestore)
	r.Get("/api/settings/notifications", s.apiGetNotifications)
	r.Put("/api/settings/notifications", s.apiPutNotifications)
	r.Get("/api/consent/start/{id}", s.handleConsentStart)
	r.Get("/api/consent/callback", s.handleConsentCallback)

	if s.Static != nil {
		r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(s.Static))))
	}
	return r
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	_ = s.Templates.ExecuteTemplate(w, "login.html", nil)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	ip := clientIP(r)
	if !s.Sessions.allowLogin(ip) {
		http.Error(w, "too many login attempts", http.StatusTooManyRequests)
		return
	}
	token, ok := s.Sessions.Login(r.FormValue("password"))
	if !ok {
		s.Sessions.recordLoginAttempt(ip)
		http.Error(w, "invalid password", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure(r),
		MaxAge:   86400,
	})
	http.Redirect(w, r, "/tenants", http.StatusFound)
}

func (s *Server) cookieSecure(r *http.Request) bool {
	if s.Cfg != nil {
		base := strings.TrimSpace(s.Cfg.PublicBaseURL)
		if strings.HasPrefix(strings.ToLower(base), "https://") {
			return true
		}
	}
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("session"); err == nil {
		s.Sessions.Logout(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/tenants", http.StatusFound)
}

func (s *Server) handleTenants(w http.ResponseWriter, r *http.Request) {
	list, err := s.DB.ListTenants(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	cached, _ := s.DB.ListTenantUsage(r.Context())
	type row struct {
		Tenant     db.Tenant
		Usage      *storage.UsageReport
		MeasuredAt time.Time
	}
	rows := make([]row, 0, len(list))
	for i := range list {
		t := list[i]
		rec := row{Tenant: t}
		if u := cached[t.ID]; u != nil && u.Report != nil {
			rec.Usage = u.Report
			rec.MeasuredAt = u.MeasuredAt
		}
		rows = append(rows, rec)
	}
	_ = s.Templates.ExecuteTemplate(w, "tenants.html", map[string]any{
		"Tenants":      rows,
		"UsageRunning": s.Usage != nil && s.Usage.Running(),
		"UsageFlash":   r.URL.Query().Get("usage"),
	})
}

func (s *Server) handleUsageRefreshAll(w http.ResponseWriter, r *http.Request) {
	if s.Usage == nil {
		http.Error(w, "usage scanner not configured", 500)
		return
	}
	if s.Usage.RefreshAll(r.Context()) {
		http.Redirect(w, r, "/tenants?usage=started", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/tenants?usage=busy", http.StatusFound)
}

func (s *Server) handleUsageRefreshOne(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if s.Usage == nil {
		http.Error(w, "usage scanner not configured", 500)
		return
	}
	if err := s.Usage.RefreshTenant(r.Context(), id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/tenants/"+id+"?usage=ok", http.StatusFound)
}

func (s *Server) handleTenantNewForm(w http.ResponseWriter, r *http.Request) {
	_ = s.Templates.ExecuteTemplate(w, "tenant_new.html", map[string]any{
		"RedirectURI": s.Cfg.PublicBaseURL + "/api/consent/callback",
	})
}

func (s *Server) handleTenantCreate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	var exp time.Time
	if v := r.FormValue("secret_expires"); v != "" {
		exp, _ = time.Parse("2006-01-02", v)
	}
	t, err := s.Tenants.Create(r.Context(), tenant.CreateInput{
		Name: r.FormValue("name"), AzureTenantID: r.FormValue("azure_tenant_id"),
		ClientID: r.FormValue("client_id"), ClientSecret: r.FormValue("client_secret"),
		SecretExpires: exp,
	})
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	_ = s.Sched.Reload(r.Context())
	http.Redirect(w, r, "/tenants/"+t.ID+"/recovery?new=1", http.StatusFound)
}

func (s *Server) handleTenantDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := s.DB.GetTenant(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	s.ensurePSTSchedule(r.Context(), id)
	jobs, _ := s.DB.ListJobs(r.Context(), id, 30)
	schedules, _ := s.DB.ListSchedules(r.Context(), id)
	snaps := s.listTenantSnapshots(r.Context(), t)
	s.annotateSnapshots(r.Context(), id, snaps)
	usage, measuredAt := s.cachedUsage(r.Context(), id)
	pstExports, _ := storage.ListPSTExports(t.KopiaRepoPath)
	retention := storage.ParseRetentionJSON(t.RetentionJSON)
	_ = s.Templates.ExecuteTemplate(w, "tenant_detail.html", map[string]any{
		"Tenant": t, "TenantID": id, "Jobs": jobs, "JobCounts": countJobs(jobs),
		"Schedules": schedules, "Snapshots": snaps, "Usage": usage, "UsageMeasuredAt": measuredAt,
		"UsageFlash": r.URL.Query().Get("usage"),
		"PSTExports": pstExports, "Retention": retention,
	})
}

func (s *Server) handleRecoveryForm(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := s.DB.GetTenant(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	_ = s.Templates.ExecuteTemplate(w, "tenant_recovery.html", map[string]any{
		"Tenant": t,
		"IsNew":  r.URL.Query().Get("new") == "1",
		"RepoPath": storage.RepoDataDir(t.KopiaRepoPath),
	})
}

func (s *Server) handleRecoveryExport(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := s.DB.GetTenant(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	_ = r.ParseForm()
	ip := clientIP(r)
	if !s.Sessions.allowLogin(ip) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	if !s.Sessions.CheckPassword(r.FormValue("admin_password")) {
		s.Sessions.recordLoginAttempt(ip)
		_ = s.Templates.ExecuteTemplate(w, "tenant_recovery.html", map[string]any{
			"Tenant": t, "RepoPath": storage.RepoDataDir(t.KopiaRepoPath),
			"Error": "Admin-Passwort falsch.",
		})
		return
	}
	_, kopiaPass, err := s.Tenants.DecryptSecrets(t)
	if err != nil {
		http.Error(w, "decrypt failed", 500)
		return
	}
	repoPath := storage.RepoDataDir(t.KopiaRepoPath)
	action := r.FormValue("action")
	if action == "download" {
		body := formatRecoverySheet(t, repoPath, kopiaPass)
		name := "m365backup-recovery-" + sanitizeFilePart(t.Name) + ".txt"
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
		_, _ = w.Write([]byte(body))
		return
	}
	_ = s.Templates.ExecuteTemplate(w, "tenant_recovery.html", map[string]any{
		"Tenant": t, "RepoPath": repoPath,
		"KopiaPassword": kopiaPass,
		"Revealed":      true,
	})
}

func formatRecoverySheet(t *db.Tenant, repoPath, kopiaPass string) string {
	var b strings.Builder
	b.WriteString("M365 Backup — offline Kopia recovery\n")
	b.WriteString("=====================================\n\n")
	b.WriteString("KEEP THIS FILE OFFLINE. Anyone with repo path + this password can decrypt all snapshots.\n\n")
	b.WriteString("Tenant name:     " + t.Name + "\n")
	b.WriteString("Tenant ID:       " + t.ID + "\n")
	b.WriteString("Azure tenant:    " + t.AzureTenantID + "\n")
	b.WriteString("Kopia repo path: " + repoPath + "\n")
	b.WriteString("Repo password:   " + kopiaPass + "\n\n")
	b.WriteString("Restore with upstream kopia CLI (no M365 Backup app / DB required):\n\n")
	b.WriteString("  export KOPIA_PASSWORD='…password above…'\n")
	b.WriteString("  kopia repository connect filesystem --path '" + repoPath + "' --readonly\n")
	b.WriteString("  kopia snapshot list --all\n")
	b.WriteString("  kopia snapshot restore <snapshot-id> /restore/target\n\n")
	b.WriteString("This is NOT the MASTER_KEY. MASTER_KEY only encrypts secrets in the app database.\n")
	return b.String()
}

func sanitizeFilePart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "tenant"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	out := b.String()
	if out == "" {
		return "tenant"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

func (s *Server) handleJobsPartial(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	jobs, err := s.DB.ListJobs(r.Context(), id, 30)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = s.Templates.ExecuteTemplate(w, "jobs_partial.html", map[string]any{
		"Jobs": jobs, "TenantID": id, "JobCounts": countJobs(jobs),
	})
}

func (s *Server) handleJobDetail(w http.ResponseWriter, r *http.Request) {
	tid := chi.URLParam(r, "id")
	jid := chi.URLParam(r, "jobID")
	t, err := s.DB.GetTenant(r.Context(), tid)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	job, err := s.DB.GetJob(r.Context(), jid)
	if err != nil || job.TenantID != tid {
		http.Error(w, "job not found", 404)
		return
	}
	logs, _ := s.DB.ListJobLogs(r.Context(), jid)
	_ = s.Templates.ExecuteTemplate(w, "job_detail.html", map[string]any{
		"Tenant": t, "Job": job, "Logs": logs,
	})
}

func (s *Server) handleJobLive(w http.ResponseWriter, r *http.Request) {
	tid := chi.URLParam(r, "id")
	jid := chi.URLParam(r, "jobID")
	t, err := s.DB.GetTenant(r.Context(), tid)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	job, err := s.DB.GetJob(r.Context(), jid)
	if err != nil || job.TenantID != tid {
		http.Error(w, "job not found", 404)
		return
	}
	logs, _ := s.DB.ListJobLogs(r.Context(), jid)
	_ = s.Templates.ExecuteTemplate(w, "job_live_partial.html", map[string]any{
		"Tenant": t, "Job": job, "Logs": logs,
	})
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	tid := chi.URLParam(r, "id")
	jid := chi.URLParam(r, "jobID")
	job, err := s.DB.GetJob(r.Context(), jid)
	if err != nil || job.TenantID != tid {
		http.Error(w, "job not found", 404)
		return
	}
	if err := s.Runner.Cancel(r.Context(), jid); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		jobs, _ := s.DB.ListJobs(r.Context(), tid, 30)
		_ = s.Templates.ExecuteTemplate(w, "jobs_partial.html", map[string]any{
			"Jobs": jobs, "TenantID": tid, "JobCounts": countJobs(jobs),
		})
		return
	}
	http.Redirect(w, r, "/tenants/"+tid+"/jobs/"+jid, http.StatusFound)
}

func (s *Server) handleSnapshotBrowse(w http.ResponseWriter, r *http.Request) {
	tid := chi.URLParam(r, "id")
	snapID := chi.URLParam(r, "snapID")
	if err := storage.ValidateSnapshotID(snapID); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	rel := r.URL.Query().Get("path")
	q := r.URL.Query().Get("q")
	t, err := s.DB.GetTenant(r.Context(), tid)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	snaps := s.listTenantSnapshots(r.Context(), t)
	s.annotateSnapshots(r.Context(), tid, snaps)
	svc := ""
	for _, sn := range snaps {
		if sn.ID == snapID {
			svc = sn.Service
			break
		}
	}
	u := "/tenants/" + tid + "/browser?version=" + url.QueryEscape(snapID)
	if svc != "" {
		u += "&service=" + url.QueryEscape(svc)
	}
	if rel != "" {
		u += "&path=" + url.QueryEscape(rel)
	}
	if q != "" {
		u += "&q=" + url.QueryEscape(q)
	}
	http.Redirect(w, r, u, http.StatusFound)
}

func (s *Server) handleSnapshotFile(w http.ResponseWriter, r *http.Request) {
	tid := chi.URLParam(r, "id")
	snapID := chi.URLParam(r, "snapID")
	if err := storage.ValidateSnapshotID(snapID); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	rel := r.URL.Query().Get("path")
	t, err := s.DB.GetTenant(r.Context(), tid)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	snaps := s.listTenantSnapshots(r.Context(), t)
	s.annotateSnapshots(r.Context(), tid, snaps)
	svc := ""
	for _, sn := range snaps {
		if sn.ID == snapID {
			svc = sn.Service
			break
		}
	}
	u := "/tenants/" + tid + "/browser/file?version=" + url.QueryEscape(snapID) + "&path=" + url.QueryEscape(rel)
	if svc != "" {
		u += "&service=" + url.QueryEscape(svc)
	}
	http.Redirect(w, r, u, http.StatusFound)
}

func (s *Server) annotateSnapshots(ctx context.Context, tenantID string, snaps []storage.SnapshotInfo) {
	jobs, _ := s.DB.ListJobs(ctx, tenantID, 200)
	m := map[string]string{}
	for _, j := range jobs {
		if j.KopiaSnapshot != "" && j.Service != "" {
			m[j.KopiaSnapshot] = j.Service
		}
	}
	storage.AnnotateServices(snaps, m)
}

func (s *Server) listTenantSnapshots(ctx context.Context, t *db.Tenant) []storage.SnapshotInfo {
	_, pass, err := s.Tenants.DecryptSecrets(t)
	if err != nil {
		return nil
	}
	snaps, err := s.Store.ListSnapshots(ctx, t.KopiaRepoPath, pass)
	if err != nil {
		return nil
	}
	return snaps
}

func (s *Server) cachedUsage(ctx context.Context, tenantID string) (*storage.UsageReport, time.Time) {
	row, err := s.DB.GetTenantUsage(ctx, tenantID)
	if err != nil || row == nil || row.Report == nil {
		return nil, time.Time{}
	}
	return row.Report, row.MeasuredAt
}

func (s *Server) measureAndStoreUsage(ctx context.Context, t *db.Tenant) *storage.UsageReport {
	if s.Usage != nil {
		if err := s.Usage.RefreshTenant(ctx, t.ID); err != nil {
			s.Log.Warn("usage refresh", "tenant", t.ID, "err", err)
		}
		u, _ := s.cachedUsage(ctx, t.ID)
		return u
	}
	snaps := s.listTenantSnapshots(ctx, t)
	s.annotateSnapshots(ctx, t.ID, snaps)
	u, err := s.Store.MeasureUsage(t.KopiaRepoPath, snaps)
	if err != nil || u == nil {
		return &storage.UsageReport{TenantID: t.ID, RepoPath: t.KopiaRepoPath, TotalHuman: "0 B"}
	}
	u.TenantID = t.ID
	_ = s.DB.UpsertTenantUsage(ctx, t.ID, u)
	return u
}

// JobCounts summarizes job statuses for the jobs UI strip.
type JobCounts struct {
	Running   int
	Success   int
	Warning   int
	Error     int
	Cancelled int
	Queued    int
}

func countJobs(jobs []db.Job) JobCounts {
	var c JobCounts
	for _, j := range jobs {
		switch j.Status {
		case "running":
			c.Running++
		case "success":
			c.Success++
		case "warning":
			c.Warning++
		case "error":
			c.Error++
		case "cancelled":
			c.Cancelled++
		case "queued":
			c.Queued++
		}
	}
	return c
}

func (s *Server) handleBrowser(w http.ResponseWriter, r *http.Request) {
	tid := chi.URLParam(r, "id")
	t, err := s.DB.GetTenant(r.Context(), tid)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	service := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("service")))
	version := strings.TrimSpace(r.URL.Query().Get("version"))
	rel := r.URL.Query().Get("path")
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	services := []string{"exchange", "onedrive", "teams", "sharepoint"}
	snaps := s.listTenantSnapshots(r.Context(), t)
	s.annotateSnapshots(r.Context(), tid, snaps)

	var versions []storage.SnapshotInfo
	hasLive := false
	if service != "" {
		versions = storage.FilterByService(snaps, service)
		_, hasLive = storage.LiveSyncRoot(t.KopiaRepoPath, service)
	}

	usage, _ := s.cachedUsage(r.Context(), tid)
	data := map[string]any{
		"Tenant": t, "Services": services, "Service": service,
		"Versions": versions, "Version": version, "HasLive": hasLive,
		"Path": rel, "Parent": "", "Entries": []storage.BrowseEntry{}, "Query": q,
		"Usage": usage,
	}

	if service != "" && version != "" {
		root, err := s.resolveBrowserRoot(r.Context(), t, service, version)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var entries []storage.BrowseEntry
		if q != "" {
			entries, err = storage.SearchBrowse(root, q, 500)
		} else {
			entries, err = storage.ListBrowseDir(root, rel)
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		parent := ""
		if rel != "" && rel != "." {
			parent = filepath.Dir(filepath.Clean(rel))
			if parent == "." {
				parent = ""
			}
		}
		data["Entries"] = entries
		data["Parent"] = parent
	}

	_ = s.Templates.ExecuteTemplate(w, "browser.html", data)
}

func (s *Server) handleBrowserFile(w http.ResponseWriter, r *http.Request) {
	tid := chi.URLParam(r, "id")
	service := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("service")))
	version := strings.TrimSpace(r.URL.Query().Get("version"))
	rel := r.URL.Query().Get("path")
	t, err := s.DB.GetTenant(r.Context(), tid)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	root, err := s.resolveBrowserRoot(r.Context(), t, service, version)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	abs, err := storage.OpenBrowseFile(root, rel)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	name := storage.DisplayNameFor(abs, filepath.Base(abs))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	http.ServeFile(w, r, abs)
}

func (s *Server) resolveBrowserRoot(ctx context.Context, t *db.Tenant, service, version string) (string, error) {
	if version == "" {
		return "", fmt.Errorf("version required")
	}
	if version == "live" {
		root, ok := storage.LiveSyncRoot(t.KopiaRepoPath, service)
		if !ok {
			return "", fmt.Errorf("kein Live-Sync für %s", service)
		}
		return root, nil
	}
	if err := storage.ValidateSnapshotID(version); err != nil {
		return "", err
	}
	_, kopiaPass, err := s.Tenants.DecryptSecrets(t)
	if err != nil {
		return "", err
	}
	return s.Store.EnsureExtracted(ctx, t.KopiaRepoPath, kopiaPass, version, s.Cfg.StagingRoot)
}

func (s *Server) handleTriggerBackup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	service := chi.URLParam(r, "service")
	jobType := "delta"
	if service == "pst" {
		jobType = "export"
	}
	_, err := s.Runner.Enqueue(r.Context(), id, service, "", jobType)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/tenants/"+id, http.StatusFound)
}

func (s *Server) handleUpdateRetention(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	tid := chi.URLParam(r, "id")
	if _, err := s.DB.GetTenant(r.Context(), tid); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	p := storage.RetentionPolicy{
		Enabled:     r.FormValue("enabled") == "on" || r.FormValue("enabled") == "true",
		KeepHours:   atoiDefault(r.FormValue("keep_hours"), 24),
		KeepDaily:   atoiDefault(r.FormValue("keep_daily"), 7),
		KeepWeekly:  atoiDefault(r.FormValue("keep_weekly"), 4),
		KeepMonthly: atoiDefault(r.FormValue("keep_monthly"), 6),
		KeepYearly:  atoiDefault(r.FormValue("keep_yearly"), 2),
		KeepMin:     atoiDefault(r.FormValue("keep_min"), 3),
		PSTKeepRuns: atoiDefault(r.FormValue("pst_keep_runs"), 5),
	}
	if err := s.DB.UpdateTenantRetention(r.Context(), tid, p.ToJSON()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/tenants/"+tid, http.StatusFound)
}

func (s *Server) handlePSTExportDownload(w http.ResponseWriter, r *http.Request) {
	tid := chi.URLParam(r, "id")
	runID := chi.URLParam(r, "runID")
	file := chi.URLParam(r, "file")
	t, err := s.DB.GetTenant(r.Context(), tid)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if strings.Contains(runID, "..") || strings.Contains(file, "..") || strings.Contains(file, "/") || strings.Contains(file, "\\") {
		http.Error(w, "invalid path", 400)
		return
	}
	path := filepath.Join(storage.PSTExportRoot(t.KopiaRepoPath), runID, file)
	root := storage.PSTExportRoot(t.KopiaRepoPath)
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		http.Error(w, "invalid path", 400)
		return
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		http.Error(w, "not found", 404)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file))
	http.ServeFile(w, r, path)
}

func atoiDefault(s string, def int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func (s *Server) ensurePSTSchedule(ctx context.Context, tenantID string) {
	schedules, err := s.DB.ListSchedules(ctx, tenantID)
	if err != nil {
		return
	}
	for _, sch := range schedules {
		if sch.Service == "pst" {
			return
		}
	}
	_ = s.DB.CreateSchedule(ctx, &db.Schedule{
		TenantID: tenantID,
		Service:  "pst",
		CronExpr: "0 4 * * 0",
		Enabled:  false,
	})
	_ = s.Sched.Reload(ctx)
}

func (s *Server) handleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	sid := chi.URLParam(r, "sid")
	tid := chi.URLParam(r, "id")
	sch, err := s.DB.GetSchedule(r.Context(), sid)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	if sch.TenantID != tid {
		http.Error(w, "not found", 404)
		return
	}
	sch.CronExpr = r.FormValue("cron_expr")
	sch.Enabled = r.FormValue("enabled") == "on" || r.FormValue("enabled") == "true" || r.FormValue("enabled") == "1"
	_ = s.DB.UpdateSchedule(r.Context(), sch)
	_ = s.Sched.Reload(r.Context())
	http.Redirect(w, r, "/tenants/"+tid, http.StatusFound)
}

func (s *Server) handleRestoreForm(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := s.DB.GetTenant(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	snaps := s.listTenantSnapshots(r.Context(), t)
	_ = s.Templates.ExecuteTemplate(w, "restore.html", map[string]any{"Tenant": t, "Snapshots": snaps})
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id := chi.URLParam(r, "id")
	snapID := r.FormValue("snapshot_id")
	mode := r.FormValue("mode")
	service := r.FormValue("service")
	if err := storage.ValidateSnapshotID(snapID); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	t, err := s.DB.GetTenant(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	_, kopiaPass, err := s.Tenants.DecryptSecrets(t)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	work, err := storage.EnsureSubpath(s.Cfg.StagingRoot, "restore-"+snapID)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	_ = os.MkdirAll(work, 0o700)
	defer func() { _ = os.RemoveAll(work) }()

	if mode == "graph" && (service == "onedrive" || service == "sharepoint") {
		dest := filepath.Join(work, "out")
		if err := s.Store.Restore(r.Context(), t.KopiaRepoPath, kopiaPass, snapID, dest); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if err := s.graphUploadRestore(r.Context(), t, service, dest); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = s.Notifier.Send(r.Context(), notification.Event{
			Type: notification.EventRestoreDone, TenantID: t.ID,
			Subject: "Graph restore done: " + t.Name, Body: "Service " + service + " snapshot " + snapID,
		})
		http.Redirect(w, r, "/tenants/"+id+"?restored=1", http.StatusFound)
		return
	}

	zipPath, err := s.Store.ExportZip(r.Context(), t.KopiaRepoPath, kopiaPass, snapID, work)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = s.Notifier.Send(r.Context(), notification.Event{
		Type: notification.EventRestoreDone, TenantID: t.ID,
		Subject: "Restore export ready: " + t.Name, Body: "Snapshot " + snapID,
	})
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", snapID+".zip"))
	http.ServeFile(w, r, zipPath)
}

func (s *Server) graphUploadRestore(ctx context.Context, t *db.Tenant, service, dest string) error {
	clientSecret, _, err := s.Tenants.DecryptSecrets(t)
	if err != nil {
		return err
	}
	gc, err := graph.New(ctx, t.AzureTenantID, t.ClientID, clientSecret)
	if err != nil {
		return err
	}
	root := filepath.Join(dest, service)
	files, err := storage.CollectFilesUnder(root, "")
	if err != nil {
		return err
	}
	users, err := gc.ListUsers(ctx)
	if err != nil || len(users) == 0 {
		return fmt.Errorf("no users for graph restore: %v", err)
	}
	uid := ""
	if users[0].GetId() != nil {
		uid = *users[0].GetId()
	}
	drive, err := gc.Graph.Users().ByUserId(uid).Drive().Get(ctx, nil)
	if err != nil {
		return err
	}
	driveID := ""
	if drive.GetId() != nil {
		driveID = *drive.GetId()
	}
	tok, err := gc.Token.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://graph.microsoft.com/.default"},
	})
	if err != nil {
		return err
	}
	for _, fpath := range files {
		rel, _ := filepath.Rel(root, fpath)
		data, err := os.ReadFile(fpath)
		if err != nil {
			continue
		}
		safeRel := strings.ReplaceAll(filepath.ToSlash(rel), " ", "_")
		uploadURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/drives/%s/root:/M365Backup-Restore/%s:/content", driveID, safeRel)
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, strings.NewReader(string(data)))
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+tok.Token)
		req.Header.Set("Content-Type", "application/octet-stream")
		resp, err := gc.HTTP.Do(req)
		if err != nil {
			s.Log.Warn("upload failed", "file", rel, "err", err)
			continue
		}
		_ = resp.Body.Close()
	}
	return nil
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.DB.ListNotificationSettings(r.Context())
	_ = s.Templates.ExecuteTemplate(w, "settings.html", map[string]any{"Settings": settings})
}

func (s *Server) handleSaveNotifications(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	cfg := map[string]any{}
	channel := r.FormValue("channel")
	switch channel {
	case "smtp":
		cfg["host"] = r.FormValue("smtp_host")
		cfg["port"], _ = strconv.Atoi(r.FormValue("smtp_port"))
		cfg["username"] = r.FormValue("smtp_username")
		cfg["password"] = r.FormValue("smtp_password")
		cfg["from"] = r.FormValue("smtp_from")
		cfg["to"] = splitCSV(r.FormValue("smtp_to"))
	case "pushover":
		cfg["user_key"] = r.FormValue("pushover_user_key")
		cfg["app_token"] = r.FormValue("pushover_app_token")
		cfg["device"] = r.FormValue("pushover_device")
		cfg["title"] = r.FormValue("pushover_title")
		cfg["sound"] = r.FormValue("pushover_sound")
		cfg["sound_ok"] = r.FormValue("pushover_sound_ok")
		prio, _ := strconv.Atoi(r.FormValue("pushover_priority"))
		cfg["priority"] = prio
		ttl, _ := strconv.Atoi(r.FormValue("pushover_ttl"))
		cfg["ttl"] = ttl
		retry, _ := strconv.Atoi(r.FormValue("pushover_retry"))
		cfg["retry"] = retry
		expire, _ := strconv.Atoi(r.FormValue("pushover_expire"))
		cfg["expire"] = expire
	default:
		whURL := r.FormValue("webhook_url")
		if err := notification.ValidateWebhookURL(whURL); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		cfg["url"] = whURL
		cfg["format"] = r.FormValue("format")
	}
	cfgJSON, _ := json.Marshal(cfg)
	notifyOn, _ := json.Marshal(splitCSV(r.FormValue("notify_on")))
	st := &db.NotificationSetting{
		Channel:  channel,
		Enabled:  true,
		Config:   string(cfgJSON),
		NotifyOn: string(notifyOn),
	}
	if err := s.DB.UpsertNotificationSetting(r.Context(), st); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusFound)
}

func (s *Server) handleConsentStart(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := s.DB.GetTenant(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	state, err := tenant.SignConsentState(id, s.Cfg.MasterKey, time.Hour)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, s.Tenants.ConsentURL(t, state), http.StatusFound)
}

func (s *Server) handleConsentCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	adminConsent := r.URL.Query().Get("admin_consent")
	tenantID, err := tenant.VerifyConsentState(state, s.Cfg.MasterKey)
	if err != nil {
		http.Error(w, "invalid state", 400)
		return
	}
	if adminConsent != "True" && adminConsent != "true" {
		http.Error(w, "consent not granted", 400)
		return
	}
	if err := s.Tenants.Activate(r.Context(), tenantID); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Kick off first full backups for all services
	for _, svc := range []string{"exchange", "onedrive", "teams", "sharepoint"} {
		_, _ = s.Runner.Enqueue(r.Context(), tenantID, svc, "", "full")
	}
	http.Redirect(w, r, "/tenants/"+tenantID+"?consent=ok", http.StatusFound)
}

func (s *Server) apiListTenants(w http.ResponseWriter, r *http.Request) {
	list, err := s.DB.ListTenants(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if r.URL.Query().Get("usage") != "1" {
		writeJSON(w, publicTenants(list))
		return
	}
	cached, _ := s.DB.ListTenantUsage(r.Context())
	type row struct {
		db.Tenant
		Usage *storage.UsageReport `json:"usage"`
	}
	out := make([]row, 0, len(list))
	for i := range list {
		t := publicTenant(list[i])
		rec := row{Tenant: t}
		if u := cached[t.ID]; u != nil {
			rec.Usage = u.Report
		}
		out = append(out, rec)
	}
	writeJSON(w, out)
}

func (s *Server) apiTenantUsage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := s.DB.GetTenant(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if r.URL.Query().Get("fresh") == "1" {
		u := s.measureAndStoreUsage(r.Context(), t)
		if u == nil {
			http.Error(w, "measure failed", 500)
			return
		}
		writeJSON(w, u)
		return
	}
	u, measuredAt := s.cachedUsage(r.Context(), id)
	if u == nil {
		writeJSON(w, map[string]any{
			"tenant_id": id,
			"cached":    false,
			"message":   "no usage cache yet; POST /api/tenants/{id}/usage/refresh or wait for hourly cron",
		})
		return
	}
	writeJSON(w, map[string]any{
		"cached":      true,
		"measured_at": measuredAt,
		"usage":       u,
	})
}

func (s *Server) apiUsageRefreshAll(w http.ResponseWriter, r *http.Request) {
	if s.Usage == nil {
		http.Error(w, "usage scanner not configured", 500)
		return
	}
	started := s.Usage.RefreshAll(r.Context())
	writeJSON(w, map[string]any{"started": started, "running": s.Usage.Running()})
}

func (s *Server) apiUsageRefreshOne(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if s.Usage == nil {
		http.Error(w, "usage scanner not configured", 500)
		return
	}
	if err := s.Usage.RefreshTenant(r.Context(), id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	u, measuredAt := s.cachedUsage(r.Context(), id)
	writeJSON(w, map[string]any{"ok": true, "measured_at": measuredAt, "usage": u})
}

func (s *Server) apiCreateTenant(w http.ResponseWriter, r *http.Request) {
	var in tenant.CreateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	t, err := s.Tenants.Create(r.Context(), in)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	_ = s.Sched.Reload(r.Context())
	writeJSON(w, publicTenant(*t))
}

func (s *Server) apiListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.DB.ListJobs(r.Context(), chi.URLParam(r, "id"), 50)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, jobs)
}

func (s *Server) apiUpdateSchedules(w http.ResponseWriter, r *http.Request) {
	tid := chi.URLParam(r, "id")
	var body []struct {
		ID       string `json:"id"`
		CronExpr string `json:"cron_expr"`
		Enabled  bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	for _, item := range body {
		sch, err := s.DB.GetSchedule(r.Context(), item.ID)
		if err != nil {
			continue
		}
		if sch.TenantID != tid {
			continue
		}
		sch.CronExpr = item.CronExpr
		sch.Enabled = item.Enabled
		_ = s.DB.UpdateSchedule(r.Context(), sch)
	}
	_ = s.Sched.Reload(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiRestore(w http.ResponseWriter, r *http.Request) {
	s.handleRestore(w, r)
}

func (s *Server) apiGetNotifications(w http.ResponseWriter, r *http.Request) {
	list, err := s.DB.ListNotificationSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, publicNotificationSettings(list))
}

func (s *Server) apiPutNotifications(w http.ResponseWriter, r *http.Request) {
	var st db.NotificationSetting
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	switch st.Channel {
	case "webhook", "slack", "teams":
		var cfg struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal([]byte(st.Config), &cfg)
		if err := notification.ValidateWebhookURL(cfg.URL); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	if err := s.DB.UpsertNotificationSetting(r.Context(), &st); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, publicNotificationSetting(st))
}

func (s *Server) handleOpenAPIPage(w http.ResponseWriter, r *http.Request) {
	_ = s.Templates.ExecuteTemplate(w, "openapi.html", map[string]any{
		"BaseURL": s.requestBaseURL(r),
	})
}

func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	if len(s.OpenAPI) == 0 {
		http.Error(w, "openapi not embedded", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(rewriteOpenAPIServers(s.OpenAPI, s.requestBaseURL(r)))
}

func (s *Server) requestBaseURL(r *http.Request) string {
	if s.Cfg != nil {
		if base := strings.TrimRight(strings.TrimSpace(s.Cfg.PublicBaseURL), "/"); base != "" {
			return base
		}
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// openAPIServersRE matches the top-level servers: block so we can inject this instance's URL.
var openAPIServersRE = regexp.MustCompile(`(?m)^servers:\n(?:[ \t].*\n)*`)

func rewriteOpenAPIServers(spec []byte, baseURL string) []byte {
	block := []byte(fmt.Sprintf("servers:\n  - url: %s\n    description: This running instance\n", baseURL))
	if openAPIServersRE.Match(spec) {
		return openAPIServersRE.ReplaceAll(spec, block)
	}
	return spec
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
