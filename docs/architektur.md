# Architektur — Überblick & Gedanken

## Warum dieses Projekt existiert

Nach dem Ende von MinIO Community Edition und dem kommerziellen Kurs von Corso fehlte ein
starkes **open-source, multi-tenant** M365-Backup für Self-Hosting / Rechenzentrum:

- viele Kunden-Tenants in **einer** Control Plane
- inkrementeller Graph-Sync + Speicher-Dedup/Verschlüsselung
- Restore **ohne** proprietäres SaaS-Backend (Repo-Pfad + Passwort)
- kein Vendor-Lock-in

M365 Backup füllt diese Lücke. Wir betreiben den Stack selbst produktiv; Community-Support
ist best-effort, kommerzieller Support möglich.

## Bausteine

```text
┌─────────────────────────────────────────────────────────────┐
│  Control Plane (ein Go-Binary)                              │
│                                                             │
│  Admin UI (HTMX) ──► HTTP API ──► Job Runner                │
│                          ▲              │                   │
│                     Cron Scheduler ─────┘                   │
│                          │                                  │
│                     SQLite / MySQL                          │
│              (Jobs, Tokens, Secrets, Schedules)             │
│                          │                                  │
│                     Notifications                           │
└──────────────────────────┼──────────────────────────────────┘
                           │
           ┌───────────────┼───────────────┐
           ▼               ▼               ▼
      Graph API      Staging (temp)   Kopia Root
      (pro Tenant)   STAGING_ROOT     KOPIA_ROOT/{tenant}/
                                         ├── repo/      Snapshots
                                         ├── sync/      Live-Bäume
                                         └── exports/   PST-Läufe
```

### Was gehört wohin?

| Schicht | Verantwortung | Was **nicht** |
|---------|---------------|---------------|
| **DB** | Metadaten, Delta-Tokens, verschlüsselte Secrets, Job-Status | Snapshot-Bytes |
| **Live-Sync (`sync/`)** | Aktuelle Arbeitskopie für schnelle Deltas | Langzeit-Historie |
| **Kopia (`repo/`)** | Verschlüsselte, deduplizierte Punkt-in-Zeit-Historie | Klartext-Arbeitsbaum |
| **Staging** | Ephemere Job-Temp-Dirs (Teams/SP, Zwischenstände) | Persistente Kundendaten |
| **UI/API** | Bedienung, Consent, Browse/Restore | Graph-Sync-Logik selbst |

**Gedanke:** Die DB ist die Steuerung, die Platte die Wahrheit der Backup-Daten.
Snapshots liegen bewusst **nicht** in SQLite/MySQL — das würde Multi-Tenant-Skalierung
und Offline-Recovery kaputtmachen.

## Kern-Datenfluss

```text
Trigger (Cron / UI / Consent)
    │
    ▼
Enqueue  ──► Tenant busy? ──► ablehnen (ErrTenantBusy)
    │
    ▼
Graph ziehen (Delta oder Full)
    │
    ├─► Exchange/OneDrive → persistenter Live-Sync-Baum
    └─► Teams/SharePoint  → Staging-Baum (pro Lauf)
    │
    ▼
Kopia-Snapshot (außer PST-Export)
    │
    ▼
Smart Recycle + GC
    │
    ▼
Job-Status in DB + optional Notification
```

Details zu Sync vs. Snapshot und Locks: [backup-logic.md](backup-logic.md).

## Package-Landkarte

| Package | Rolle |
|---------|--------|
| `cmd/server` | Composition Root — verdrahtet alles |
| `internal/config` | Env → Config-Struct |
| `internal/crypto` | AES-256-GCM für DB-Secrets |
| `internal/db` | Persistenz Control Plane |
| `internal/tenant` | Lifecycle, Consent, Default-Schedules, Keycheck |
| `internal/graph` | App-Only Graph-Client |
| `internal/backup` | Registry, Runner, Scheduler, Service-Implementierungen |
| `internal/storage` | Kopia-Engine, Browse, Retention, Restore, Usage |
| `internal/notification` | SMTP / Webhook / Pushover + SSRF-Filter |
| `internal/api` | chi-Router, Sessions, HTMX-Handler |
| `web` | Embedded Templates, Static, OpenAPI |

## Design-Entscheidungen (kurz)

1. **Ein Binary, kein Microservice-Zoo** — Betrieb und Debugging bleiben einfach.
2. **Echte Kopia-Library** (nicht eigenes Tar-Format) — Dedup, Encryption, CLI-Recovery „gratis“.
3. **Filesystem-Backend zuerst** — klar für DC/Bind-Mounts; S3/Offsite bewusst später.
4. **HTMX + Go-Templates embedded** — Admin-UI ohne Node-Build-Pipeline.
5. **Shared Admin-Passwort** — bewusst simpel für Early-OSS; RBAC/2FA sind Roadmap.

## Was noch dünn ist

Teams (kein echtes Delta, keine Attachments), SharePoint (nur Root-Children), kein Binary-`.pst`,
kein Offsite-Backend. Die Architektur lässt Erweiterungen zu, ohne den Kern (Live-Sync + Kopia)
umzubauen — neue Dienste implementieren `ServiceBackup` und melden sich in der Registry an.
