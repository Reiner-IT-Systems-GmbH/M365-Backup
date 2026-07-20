package main

import (
	"context"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/rhw/m365backup/internal/api"
	"github.com/rhw/m365backup/internal/backup"
	"github.com/rhw/m365backup/internal/config"
	"github.com/rhw/m365backup/internal/crypto"
	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/notification"
	"github.com/rhw/m365backup/internal/storage"
	"github.com/rhw/m365backup/internal/tenant"
	"github.com/rhw/m365backup/web"
)

func main() {
	_ = godotenv.Load()
	// Text logs are easier to follow with `docker compose logs -f`
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	cipher, err := crypto.New(cfg.MasterKey)
	if err != nil {
		log.Error("master key", "err", err)
		os.Exit(1)
	}
	database, err := db.Open(cfg.DBOptions())
	if err != nil {
		log.Error("database", "err", err)
		os.Exit(1)
	}
	defer database.Close()

	for _, dir := range []string{cfg.KopiaRoot, cfg.StagingRoot} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			log.Error("mkdir", "dir", dir, "err", err)
			os.Exit(1)
		}
	}

	store := storage.NewEngine()
	tenants := &tenant.Manager{
		DB: database, Cipher: cipher, KopiaRoot: cfg.KopiaRoot, Store: store, BaseURL: cfg.PublicBaseURL,
	}
	notifier := notification.New(database, log)
	notifier.SMTPHost = cfg.SMTPHost
	notifier.SMTPPort = cfg.SMTPPort
	notifier.SMTPUser = cfg.SMTPUsername
	notifier.SMTPPassword = cfg.SMTPPassword
	notifier.SMTPFrom = cfg.SMTPFrom
	notifier.SMTPTo = cfg.SMTPTo

	reg := backup.NewRegistry(
		backup.ExchangeBackup{Workers: cfg.ExchangeWorkers},
		backup.OneDriveBackup{Workers: cfg.ExchangeWorkers},
		backup.TeamsBackup{},
		backup.SharePointBackup{},
		backup.PSTExport{},
	)
	runner := backup.NewRunner(database, tenants, reg, store, notifier, cfg.StagingRoot, cfg.MaxConcurrentJobs, log)
	runner.RecoverOrphans(context.Background())
	usageScan := backup.NewUsageScanner(database, store, tenants, log)
	sched := backup.NewScheduler(database, runner, log)
	sched.Usage = usageScan
	if err := sched.Start(context.Background()); err != nil {
		log.Error("scheduler", "err", err)
		os.Exit(1)
	}
	defer sched.Stop()

	tmpl, err := template.ParseFS(web.Templates, "templates/*.html")
	if err != nil {
		log.Error("templates", "err", err)
		os.Exit(1)
	}
	staticRoot, err := fs.Sub(web.Static, "static")
	if err != nil {
		log.Error("static", "err", err)
		os.Exit(1)
	}

	srv := &api.Server{
		Cfg: cfg, DB: database, Tenants: tenants, Runner: runner, Sched: sched,
		Store: store, Notifier: notifier, Sessions: api.NewSessionStore(cfg.AdminPassword),
		Usage: usageScan, Templates: tmpl, Static: staticRoot, OpenAPI: web.OpenAPI, Log: log,
	}

	httpSrv := &http.Server{Addr: cfg.HTTPAddr, Handler: srv.Router(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("listening", "addr", cfg.HTTPAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("http", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	log.Info("shutdown complete")
}
