# Datenbank — Control Plane

Die DB speichert **Steuerung und Metadaten**, keine Snapshot-Payloads.

## Dual-Driver

| Driver | Wann | Besonderheit |
|--------|------|--------------|
| SQLite | Dev / kleine Deployments | `MaxOpenConns=1` (einfach, sicher) |
| MySQL | Docker Compose / Prod | Pool 25, Ping-Retry, utf8mb4 |

Schema-Migrationen laufen beim Open. MySQL: Duplicate-Column tolerant (DDL auto-commit).

## Migrationen (Überblick)

| Version | Inhalt |
|---------|--------|
| `001_init` | tenants, schedules, jobs, delta_tokens, notification_* |
| `002_job_logs` | Live-Logs pro Job |
| `003_job_progress` | `progress_pct`, `progress_message` |
| `004_retention` | `tenants.retention_json` |
| `005_usage_cache` | `tenant_usage` |
| `006_job_params` | `jobs.params` |
| `007_users` | users, api_tokens (bcrypt, Bearer-Scopes) |
| `008_job_active_lock` | ein aktiver Job pro Tenant+Service |
| `009_catalog` | catalog_snapshots, catalog_items, catalog_changes |
| `010_store_rename` | `store_password`, `store_path`, `jobs.snapshot_id` |

## Kern-Entities

### Tenant

| Feld (Konzept) | Bedeutung |
|----------------|-----------|
| Status `setup` | Angelegt, Consent noch ausstehend — Scheduler ignoriert |
| Status `active` | Consent ok — Cron darf Jobs starten |
| `client_secret` | encrypted |
| `store_password` | encrypted Store-Passwort |
| `store_path` | `{STORE_ROOT}/{tenant-uuid}` (Store-Root) |
| `retention_json` | Smart-Recycle-Policy |

### Schedule

Pro Tenant **und** Service: `cron_expr`, `enabled`, `last_run`.
Defaults siehe [backup-logic.md](backup-logic.md) — bestehende Zeilen werden nie still überschrieben.

### Job

```text
queued → running → success | warning | error | cancelled
```

| Feld | Bedeutung |
|------|-----------|
| `job_type` | typisch `delta`, `full`, `export` |
| `snapshot_id` | Katalog-Snapshot-ID oder PST-Run-Basename |
| Progress-Felder | für HTMX-Live-UI |

### DeltaToken

`UNIQUE (tenant_id, service, user_id)` — speichert Graph-Delta-Links.

Beispiele für Keys:

| Service | Key-Muster |
|---------|------------|
| Exchange | `userID\|folderID` |
| OneDrive | `userID` |
| Teams / SharePoint | Marker `sync-{jobID}` (kein echtes Delta) |

Prefix `full-` / `sync-` als Legacy → Reset beim nächsten Lauf (Migrationspfad alter Tokens).

### JobLog

Levels: `info` \| `warn` \| `error` \| `skip`.  
`skip` wird oft **nicht** persistiert (Rauschen vermeiden, z. B. leere Drives).

### TenantUsage

Gecachtes Usage-Report-JSON — damit die Statistik-UI nicht jedes Mal `du` über alle Stores macht.

## Was die DB bewusst nicht ist

- Kein Blob-Store für EMLs/Dateien
- Kein Ersatz für die Snapshot-Historie auf Disk (Manifeste + Blobs)
- Kein Audit-Log / RBAC (noch Roadmap)

**Gedanke:** Control Plane schlank halten → Backup-Daten skalieren auf der Platte, nicht in der DB.
