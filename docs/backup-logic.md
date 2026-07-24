# Backup-Logik: Sync, Snapshots, Scheduler, Locks

> Teil der Design-Docs — Übersicht: [README.md](README.md) · Jobs/Dienste: [backup-jobs.md](backup-jobs.md) · Speicher: [speicher-kopia.md](speicher-kopia.md)

Dieses Dokument beschreibt, wie M365 Backup Daten speichert, inkrementell sichert,
Jobs plant und Überlappungen verhindert — inkl. **warum** Live-Sync und Snapshots
nebeneinander liegen und wozu der Tenant-Lock da ist.

## Überblick

```text
Graph (delta) ──► Live-Sync-Baum (sync/<service>/)
                        │
                        ▼
                 Kopia-Snapshot (repo/)
                        │
                        ▼
              Smart Recycle + GC
```

| Begriff | Bedeutung |
|--------|-----------|
| **Live-Sync** | Persistenter Arbeitsbaum unter `{KOPIA_ROOT}/{tenant}/sync/` — Grundlage für Graph-Delta |
| **Snapshot** | Verschlüsselter Punkt-in-Zeit in Kopia (`repo/`) — dedupliziert |
| **Job** | Ein Lauf eines Dienstes (`exchange`, `onedrive`, …) — queued → running → success/… |
| **Schedule** | Cron-Ausdruck pro Tenant+Dienst |

---

## Speicherlayout (pro Tenant)

```text
{KOPIA_ROOT}/{tenant-id}/
  repo/              Kopia-Repository (echte Snapshot-Bytes, dedup)
  kopia.config
  .kopia-cache/
  sync/
    exchange/        Live-Mailboxen (EML-Bäume)
    onedrive/        …
  exports/pst/       PST-/EML-ZIP-Exporte
```

### Warum Live-Sync + Snapshots ≈ Gesamtgröße?

- **Live-Sync** ist die aktuelle Arbeitskopie (Klartext-Dateien für schnelle Deltas).
- **Snapshots** sind die Historie; unveränderte Dateien werden in Kopia nur einmal gespeichert.
- Beide liegen **nebeneinander** auf der Platte → `Gesamt ≈ Sync + Snapshots` (plus Cache/Exports).

Die Statistik-Spalte **„Snaps (logisch)“** summiert die Inhaltsgröße je Snapshot-Manifest
(`TotalFileSize`). Das ist **nicht** der Plattenverbrauch — der steht unter „Snapshots (Kopia, dedup)“.

### Zwei Inkrement-Ebenen

1. **Microsoft Graph Delta** — nach dem ersten Full-Sync nur Änderungen in den Live-Baum.
2. **Kopia Content-Addressing** — Snapshot speichert nur neue/geänderte Blöcke.
   Dafür muss der Uploader den **letzten Snapshot derselben Source** kennen
   (`FindPreviousManifests` → `Upload(..., previous...)`). Sonst Full-Scan trotz Dedup.

---

## Default-Cron (automatisch)

Beim Anlegen eines Tenants und beim Start der App (sowie beim Öffnen der Tenant-Seite)
stellt `EnsureDefaultSchedules` fehlende Schedule-Zeilen her:

| Dienst | Default-Cron | Enabled |
|--------|--------------|---------|
| exchange | `0 * * * *` (stündlich :00) | ja |
| onedrive | `30 2 * * *` (täglich 02:30) | ja |
| teams | `0 3 * * *` (täglich 03:00) | ja |
| sharepoint | `30 3 * * 0` (So 03:30) | ja |
| pst | `0 5 * * 0` (So 05:00) | nein |

Bestehende Zeilen (Cron/Enabled) werden **nicht** überschrieben.
Speichert man ein Schedule mit **leerem** Cron-Feld, wird der Default für den Dienst gesetzt.

Zeiten sind bewusst gestaffelt, damit nicht alle Dienste in derselben Minute feuern.

---

## Job-Lock (kein paralleles Backup pro Tenant)

**Regel:** Pro Tenant darf höchstens **ein** Job in `queued` oder `running` sein.

### Warum?

- Exchange/OneDrive schreiben in denselben Tenant-Baum und dasselbe Kopia-Repo.
- Überlappende Läufe (z. B. stündliches Exchange, während der vorige Lauf noch läuft)
  erzeugen Lastspitzen, doppelte Graph-Arbeit und riskante parallele Snapshots.

### Umsetzung

1. **Enqueue-Lock** (`Runner.Enqueue`): prüft `CountActiveJobs(tenant)`; wenn > 0 →
   `ErrTenantBusy` (kein neuer Job).
2. **Prozess-Mutex** um den Enqueue-Check, damit zwei Cron-Fires nicht gleichzeitig
   „frei“ sehen.
3. **Tenant-Gate** während `runJob`: exklusives Mutex pro Tenant für die Laufzeit
   (zusätzliche Absicherung).
4. **Global** `MAX_CONCURRENT_JOBS`: begrenzt parallele Jobs **über alle Tenants**
   (Semaphore). Default oft `2` — sinnvoll bei mehreren Kunden-Tenants; pro Tenant
   greift trotzdem der Lock oben.

### Verhalten bei Konflikt

| Auslöser | Reaktion |
|----------|----------|
| Cron, Tenant busy | Log `scheduler skip (tenant busy)`, kein neuer Job; `last_run` wird gesetzt |
| UI „Backup starten“, busy | Redirect `?job=busy`, Hinweis in der UI |
| Nach Admin-Consent | Nur **Exchange** wird einmal gestartet; andere Dienste folgen über Cron |

---

## Job-Lebenszyklus

```text
Enqueue ──► queued ──► (sem + tenant gate) ──► running
                                              │
                    ┌─────────────────────────┼─────────────────────────┐
                    ▼                         ▼                         ▼
                 success                   warning                     error
                                              │
                                         cancelled
```

Nach erfolgreichem Service-Lauf: Kopia-Snapshot (außer PST-Export) → Smart Recycle → optional GC.

Beim Prozessstart: `RecoverOrphans` markiert hängengebliebene `queued`/`running` als `error`.

---

## Browser / Snapshots-UI

- Snapshot-**Liste** wird kurz gecacht (TTL), damit die Tenant-Seite nicht jedes Mal
  das Repo öffnen muss.
- Browse/Download historischer Snapshots läuft über Kopias Virtual-FS (**ohne** Full-Extract
  der gesamten Mailbox auf Staging).
- **Live-Sync** im Dateibrowser ist der schnellste Weg zum aktuellen Stand.

---

## Retention (Smart Recycle)

Pro Dienst: alle Versionen in den letzten *N* Stunden, danach höchstens eine pro Tag /
Woche / Monat / Jahr (+ Mindestanzahl). Gelöschte Manifeste werden per Kopia-Maintenance/GC
freigegeben.

---

## Konfiguration (Auszug)

| Variable | Rolle |
|----------|--------|
| `MAX_CONCURRENT_JOBS` | Max. parallele Jobs global (verschiedene Tenants) |
| `EXCHANGE_WORKERS` | Parallele Mailbox-Worker **innerhalb** eines Exchange-Jobs |
| `KOPIA_ROOT` | Wurzel der Tenant-Repos |

Siehe auch `.env.example` und `README.md`.
