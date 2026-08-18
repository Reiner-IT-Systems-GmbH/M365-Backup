# Backup-Jobs & Dienste

Package: `internal/backup` — Registry, Runner, Scheduler, Service-Implementierungen.

## Service-Registry

Jeder Dienst implementiert `ServiceBackup`:

```text
Name() string
Run(ctx, graphClient, tenant, job, stageDir, tokens, catalog) → Result
```

Registriert in `main`:

| Service | Sync-Modell | Snapshot-Quelle |
|---------|-------------|-----------------|
| **Exchange** | Graph-Delta → Katalog + Blobs | `CommitSnapshot` |
| **OneDrive** | Drive-Delta → Katalog + Blobs | `CommitSnapshot` |
| **Teams** | Full-Pull + Reconcile | `CommitSnapshot` |
| **SharePoint** | Root-Children + Reconcile | `CommitSnapshot` |
| **PST** | Liest Exchange-Katalog → `exports/pst/` | **kein** Snapshot (`SkipSnapshot`) |

Neue Dienste = neue Implementierung + Registry-Eintrag. Der Runner bleibt unverändert.

## Result — was der Runner daraus macht

| Feld | Bedeutung |
|------|-----------|
| `SkipSnapshot` | Kein Katalog-Snapshot (PST) |
| `ExportPath` | Artefakt-Pfad für UI |
| `Warnings` | Job endet als `warning` (nicht hart `error`) |
| Logs | Persistiert (außer Level `skip`) |

**Gedanke:** Services entscheiden *was* gesichert wird; der Runner entscheidet *wie* (Lock,
Snapshot, Retention, Notify). Trennung hält Graph-Code und Storage-Code auseinander.

## Runner-Ablauf (`runJob`)

```text
Semaphore erwerben (MAX_CONCURRENT_JOBS)
  → Abbruch, falls Job schon cancelled/error
  → Tenant-Gate locken
  → status=running, Progress-Context
  → Secrets entschlüsseln
  → Graph-Client (außer PST)
  → catalog.EnsureMigrated (sync/ → Generation 1, sonst empty → Full)
  → stageDir = {STAGING_ROOT}/{jobID}  (defer RemoveAll)
  → svc.Run(...)
  → SkipSnapshot? → PST-Retention, fertig
  → CommitSnapshot(service)   ← Generation + verschlüsseltes Manifest
  → ApplySmartRetention (+ GC)
  → success | warning | error + Notify
```

Progress grob: 1 % Starting → 2 % Running → 95 % Snapshot → 100 %.
Dazwischen melden Services über `Progress.Emit` / `SyncJob` (Zähler + Prozent für die UI-Bar).

## Locks — drei Ebenen

Siehe ausführlich [backup-logic.md](backup-logic.md). Kurz:

1. **Enqueue-Check** (`CountActiveJobs(tenant, service)` + Full-Sync blockiert Inkremente) → `ErrTenantBusy`
2. **DB Unique Index** — ein aktiver Job pro Tenant+Service
3. **`enqueueMu`** — zwei Cron-Fires sehen nicht gleichzeitig „frei“
4. **Service-Gate** + Datei-Lock `{store}/.locks/{service}.lock` während des Laufs
5. **Instance-Lock** — nur ein Prozess
6. **Catalog Commit** — parallele Dienste serialisieren Generationen über Job-Gate + Datei-Lock
7. **Global-Semaphore** über Tenants hinweg

## Orphans beim Start

`RecoverOrphans`: Jobs in `queued`/`running` nach Crash → `error` („interrupted by process restart“),
Staging purgen.

**Gedanke:** Nach Restart lieber klar fehlgeschlagen + neu planen als „ewig running“ in der UI.

## Scheduler

- `robfig/cron` lädt enabled Schedules; nur `tenant.Status == active`
- Leerer Tenant-Store: erstes Cron/UI-Inkrement wird `full` und zieht alle enabled Graph-Dienste nach
- Busy → Log `scheduler skip (service busy)`, `last_run` wird trotzdem gesetzt (kein Cron-Storm)
- Zusätzlich: Keycheck ~08:00, Usage ~`:15` jede Stunde, Startup-Usage nach ~45 s

## Pro Dienst — Absicht & Grenzen

### Exchange

- Persistenter EML-Katalog; Delta pro Ordner (`userID|folderID`)
- Shared Mailboxes: `accountEnabled=false` bewusst **nicht** ausgefiltert
- MIME über `messages/{id}/$value`; bei `ErrorMimeContentConversionFailed` (u. a.) JSON+Attachments-Fallback → rekonstruiertes `.eml`; Removed → Katalog-`Delete`
- Worker-Pool (`EXCHANGE_WORKERS`) für parallele Mailboxen
- Erster Sync kann schwer sein → deshalb nach Consent zuerst nur Exchange

### OneDrive

- Persistenter Datei-Katalog; Delta pro User
- Leere Drives: oft still übersprungen (kein Log-Spam)
- Restore: ZIP oder Graph-Upload nach `M365Backup-Restore/`

### Teams

- Full-Pull jedes Mal (kein brauchbares Delta im aktuellen Stand)
- Channel-Messages als `messages.json` + `messages.html`
- **Attachments werden nicht heruntergeladen**; 1:1/Chats fehlen noch
- Token nur als Marker — Platzhalter für spätere echte Inkremente

### SharePoint

- Nur Root-Drive-Children (keine tiefe Rekursion / kein echtes Delta)
- Bewusst „besser etwas als nichts“; Vertiefung ist Roadmap

### PST-Export

- **Kein** binäres Outlook-`.pst` (kein OSS-Writer) — ZIP aus EML-Bäumen
- Braucht vorhandene Exchange-Katalog-Items
- Scope (Job-`params` JSON): **alle** Postfächer, **ein** Postfach oder **ein Ordner** eines Postfachs
- Liegt unter `exports/pst/{run}/`, eigene Retention (`PSTKeepRuns`), kein Katalog-Snapshot
- Geplante PST-Läufe exportieren weiterhin alles; die UI unter „PST-Exporte“ steuert den manuellen Scope

**Gedanke hinter SkipSnapshot:** PST ist ein abgeleitetes Artefakt aus schon gesicherten EMLs.
Nochmal als Katalog-Generation zu speichern verdoppelt Speicher ohne Mehrwert.
