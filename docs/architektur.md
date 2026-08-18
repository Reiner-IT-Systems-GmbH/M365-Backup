# Architektur — Überblick & Gedanken

## Warum dieses Projekt existiert

Nach dem Ende von MinIO Community Edition und dem kommerziellen Kurs von Corso fehlte ein
starkes **open-source, multi-tenant** M365-Backup für Self-Hosting / Rechenzentrum:

- viele Kunden-Tenants in **einer** Control Plane
- inkrementeller Graph-Sync + Speicher-Dedup/Verschlüsselung
- Restore **ohne** proprietäres SaaS-Backend (Store-Pfad + Store-Passwort)
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
│              (Jobs, Tokens, Secrets, Catalog, Schedules)    │
│                          │                                  │
│                     Notifications                           │
└─────────────────────────────────────────────────────────────┘
                           │
           ┌───────────────┼───────────────┐
           ▼               ▼               ▼
      Graph API      Staging (temp)   Store-Root
      (pro Tenant)   STAGING_ROOT     STORE_ROOT/{tenant}/
                                         ├── blobs/      CAS (AES-GCM)
                                         ├── manifests/  Generationen
                                         └── exports/    PST-Läufe
```

### Was gehört wohin?

| Schicht | Verantwortung | Was **nicht** |
|---------|---------------|---------------|
| **DB** | Metadaten, Delta-Tokens, verschlüsselte Secrets, Job-Status, Katalog-Index | Blob-Bytes |
| **Blobs + Manifeste** | Verschlüsselte, deduplizierte Inhalte und Offline-Restore | Klartext-Arbeitsbaum |
| **Staging** | Ephemere Job-Temp-Dirs | Persistente Kundendaten |
| **UI/API** | Bedienung, Consent, Browse/Restore | Graph-Sync-Logik selbst |

**Gedanke:** Die DB ist die Steuerung plus Item-Index; die Platte hält die Backup-Bytes.
Blobs liegen bewusst **nicht** in SQLite/MySQL — das würde Multi-Tenant-Skalierung
und Offline-Recovery kaputtmachen.

## Kern-Datenfluss

```text
Trigger (Cron / UI / Consent)
    │
    ▼
Enqueue  ──► same service busy OR full-sync active (incrementals)? ──► ablehnen (ErrTenantBusy)
    │
    ▼
Graph ziehen (Delta oder Full)
    │
    ▼
catalog.Put / Delete  →  blobs/ (SHA-256 CAS, AES-256-GCM)
    │
    ▼
CommitSnapshot (Generation + Manifest) — außer PST-Export
    │
    ▼
Smart Recycle + Blob-GC
    │
    ▼
Job-Status in DB + optional Notification
```

Details zu Katalog, Cron und Locks: [backup-logic.md](backup-logic.md).

## Package-Landkarte

| Package | Rolle |
|---------|--------|
| `cmd/server` | Composition Root — verdrahtet alles |
| `cmd/restore` | Offline-Restore aus Manifest + Blobs (`m365-restore`) |
| `internal/config` | Env → Config-Struct |
| `internal/crypto` | AES-256-GCM für DB-Secrets |
| `internal/db` | Persistenz Control Plane + Katalog-Tabellen |
| `internal/catalog` | Item-Katalog, Manifeste, Import, Retention |
| `internal/blobstore` | Content-addressed AES-256-GCM Blobs |
| `internal/tenant` | Lifecycle, Consent, Default-Schedules, Keycheck |
| `internal/graph` | App-Only Graph-Client |
| `internal/backup` | Registry, Runner, Scheduler, Service-Implementierungen |
| `internal/storage` | ZIP, Pfad-Guards, EML-Meta, Usage-`du` |
| `internal/notification` | SMTP / Webhook / Pushover + SSRF-Filter |
| `internal/api` | chi-Router, Sessions, HTMX-Handler |
| `web` | Embedded Templates, Static, OpenAPI |

## Design-Entscheidungen (kurz)

1. **Ein Binary, kein Microservice-Zoo** — Betrieb und Debugging bleiben einfach.
2. **Eigener Katalog + CAS** — Dedup und Encryption ohne Fremd-Repo; Offline-Recovery mit `m365-restore`.
3. **Filesystem-Backend zuerst** — klar für DC/Bind-Mounts; S3/Offsite bewusst später.
4. **HTMX + Go-Templates embedded** — Admin-UI ohne Node-Build-Pipeline.
5. **Shared Admin-Passwort** — bewusst simpel für Early-OSS; RBAC/2FA sind Roadmap.

## Was noch dünn ist

Teams (kein echtes Delta, keine Attachments), SharePoint (nur Root-Children), kein Binary-`.pst`,
kein Offsite-Backend. Die Architektur lässt Erweiterungen zu, ohne den Kern (Katalog + Blobs)
umzubauen — neue Dienste implementieren `ServiceBackup` und melden sich in der Registry an.
