# Startup & Konfiguration

## Boot-Sequenz (`cmd/server/main.go`)

Alles startet in einem Composition Root — **kein** DI-Framework. Die Reihenfolge ist Absicht:

```text
1. godotenv.Load()          optional .env (Dev/Compose)
2. config.Load()            fail-fast ohne MASTER_KEY / ADMIN_PASSWORD
3. crypto.New(MasterKey)    AES-256-GCM Cipher
4. db.Open(...)             SQLite oder MySQL + Migrations
5. MkdirAll(KopiaRoot, StagingRoot)  Mode 0700
6. storage.NewEngine()
7. tenant.Manager{...}
8. notification.New + SMTP aus Config
9. backup.NewRegistry(...)  Exchange, OneDrive, Teams, SharePoint, PST
10. backup.NewRunner(...)
11. RecoverOrphans()        hängende Jobs → error, Staging purgen
12. Scheduler.Start()       Cron + Keycheck + Usage-Scan
13. Templates/Static embed → api.Server → ListenAndServe
14. SIGINT/SIGTERM → Shutdown (15s)
```

### Warum diese Reihenfolge?

| Schritt | Gedanke |
|---------|---------|
| Config/Crypto vor DB | Ohne gültigen Master-Key darf nichts starten — Secrets wären nutzlos |
| Orphans **vor** Scheduler | Sonst könnte Cron neue Jobs starten, während alte „running“-Geister existieren |
| Staging ≠ KopiaRoot | Job-Temp und persistente Kundendaten bleiben getrennt |
| Text-`slog` | Leichter lesbar mit `docker compose logs -f` |

## Wichtige Env-Variablen

| Variable | Default | Bedeutung |
|----------|---------|-----------|
| `HTTP_ADDR` | `:8080` | Listen-Adresse |
| `PUBLIC_BASE_URL` | `http://localhost:8080` | Consent-Redirect, Secure-Cookie-Flag |
| `MASTER_KEY` | — | **Pflicht**, base64 → genau 32 Bytes |
| `ADMIN_PASSWORD` | — | **Pflicht**, min. 8 Zeichen |
| `DB_DRIVER` | `sqlite` | `sqlite` oder `mysql` |
| `DATABASE_PATH` | `./data/m365backup.db` | SQLite-Datei |
| `MYSQL_DSN` / `MYSQL_*` | — | MySQL (Compose/Prod) |
| `KOPIA_ROOT` | `./data/kopia` | Wurzel aller Tenant-Repos |
| `STAGING_ROOT` | `./data/staging` | Ephemere Job-Verzeichnisse |
| `MAX_CONCURRENT_JOBS` | `2` | Globales Semaphore (verschiedene Tenants) |
| `EXCHANGE_WORKERS` | `6` (max 32) | Parallele Mailbox-/Drive-Worker **innerhalb** eines Jobs |
| `SMTP_*` | — | Fallback-Notifier (siehe Benachrichtigungen) |

Vollständige Beispiele mit Platzhaltern: [`.env.example`](../.env.example).

### Zwei Parallelitäts-Hebel

```text
MAX_CONCURRENT_JOBS   → wie viele Tenants gleichzeitig backupen dürfen
EXCHANGE_WORKERS      → wie viele Mailboxen/Drives parallel in EINEM Job
```

**Gedanke:** Global drosseln wir die Maschine (CPU/IO/Graph-Throttle), lokal parallelisieren
wir innerhalb eines Tenant-Jobs, weil Graph und Disk das oft vertragen — aber **nie** zwei
Jobs desselben Tenants gleichzeitig (siehe [backup-logic.md](backup-logic.md)).

## Dependency-Richtung

```text
api ──► backup, tenant, storage, notification, db, config
backup ──► tenant, storage, graph, db, notification
tenant ──► db, crypto, storage
storage ──► (Kopia library)
```

Die API kennt den Runner; der Runner kennt keine HTTP-Typen. Das hält Tests und CLI-Recovery
sauber getrennt von der UI.
