# Backup-Logik: Katalog, Snapshots, Scheduler, Locks

> Teil der Design-Docs — Übersicht: [README.md](README.md) · Jobs/Dienste: [backup-jobs.md](backup-jobs.md) · Speicher: [speicher-katalog.md](speicher-katalog.md)

Dieses Dokument beschreibt, wie M365 Backup Daten speichert, inkrementell sichert,
Jobs plant und Überlappungen verhindert.

## Überblick

```text
Graph (delta) ──► catalog.Put / Delete ──► blobs/ (AES-GCM CAS)
                        │
                        ▼
              CommitSnapshot (Generation + Manifest)
                        │
                        ▼
              Smart Recycle + Blob-GC
```

| Begriff | Bedeutung |
|--------|-----------|
| **Aktuell** | Live-Zeilen in `catalog_items` (`deleted=0`) |
| **Snapshot** | Eine Generation (`catalog_snapshots` + Manifest auf Disk) |
| **Job** | Ein Lauf eines Dienstes — queued → running → success/… |
| **Schedule** | Cron-Ausdruck pro Tenant+Dienst |

---

## Speicherlayout (pro Tenant)

```text
{STORE_ROOT}/{tenant-id}/
  blobs/{hh}/{sha256}
  manifests/{service}/{generation}.json.zst
  exports/pst/
```

Gesamt-Plattenbedarf ≈ Blobs (dedup) + Manifeste + Exports. Kein zweiter Klartext-Baum.

### Inkrement-Ebenen

1. **Microsoft Graph Delta** — nach dem ersten Full-Sync nur Änderungen (neue/gelöschte/verschobene Items), **pro Ordner** (Exchange) bzw. pro Drive (OneDrive). Ohne gespeicherten `deltaLink` läuft der Job den Ordner erneut vollständig durch (Folder-Replay). `odata.nextLink` wird nach jeder Delta-Seite checkpointiert, damit Abbrüche nicht bei Item 0 neu starten.
2. **Kein Content-Re-Fetch** — existiert die Graph-ID schon mit Blob-Hash im Katalog (Exchange) bzw. gleicher Dateigröße (OneDrive), wird MIME/`/content` nicht erneut von Graph geholt. Der Skip-Pfad liest nur SQL, kein `os.Stat` auf dem Blob-Store. Ordner-/Betreff-Moves aktualisieren nur den Katalog. Body-Edits an bestehenden Mails werden damit nicht erkannt (selten); ein späteres Full mit fehlendem Blob holt sie nach.
3. **Content-Addressed Blobs** — gleicher SHA-256 wird nicht zweimal geschrieben.
4. **Kein Snapshot ohne Änderungen** — `CommitSnapshot` überspringt Generation + Manifest, wenn der Lauf nichts in `pending` hatte (typisches No-Op-Inkrement).
5. **Manifest-Schreiben** — bei Änderungen streamt der Commit nur `path/hash/size` aus SQL (kein volles `ListLiveItems` mit Subject etc. im RAM) und meldet Fortschritt (~alle 5 s), damit der Job nicht bei 95 % „einfriert“.

Ist der Katalog leer und kein `sync/` mehr da, löscht der Runner die Delta-Tokens und macht einen Graph-Full (`job_type=full`). Scheduler- und UI-Inkremente werden in diesem Fall zu Full. Fehlt der Katalog tenant-weit (kein Snapshot, kein `sync/`, keine Blobs), startet der erste Lauf einen Full-Sync **aller** enabled Graph-Dienste. Alte `repo/`-Snapshots werden **nicht** importiert.

**Parallelität:** `MAX_CONCURRENT_JOBS` begrenzt parallele Jobs global (über Tenants). Mailbox-Parallelität innerhalb eines Exchange-Jobs steuert `EXCHANGE_WORKERS` — das sind unabhängige Limits. Fortschritt in der UI kommt zeitbasiert (~5 s) oder alle 250 Items; Graph-SDK-Seiten haben ein Request-Timeout (kein unbegrenzt hängender Delta-Call).

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

## Job-Lock (kein paralleles Backup pro Dienst)

**Regel:** Pro Tenant+Service darf höchstens **ein** Job in `queued` oder `running` sein.
Läuft ein **Full-Sync** (`job_type=full`), dürfen **keine Inkremente** (Cron/UI, jeder
Dienst) für denselben Tenant starten — sonst schreiben sie in einen halbfertigen
Katalog und können eine weitere unfertige Generation ablegen.

Unterschiedliche Dienste dürfen parallel laufen, **solange kein Full-Sync aktiv ist**
(z. B. zwei Fulls Exchange+OneDrive aus einem UI-Klick).

### Warum?

- Zwei Exchange-Läufe gleichzeitig verdoppeln Graph-Last und schreiben in denselben Katalog.
- Ein stündliches Inkrement während eines mehrstündigen Full-Syncs erzeugt „Datenmüll“,
  weil Tokens fehlen oder der Katalog noch nicht konsistent ist.

### Umsetzung

1. **Enqueue-Lock** (`Runner.Enqueue`): `CountActiveJobs(tenant, service)` > 0 →
   `ErrTenantBusy`. Zusätzlich: Inkrement/Export, wenn `CountActiveFullJobs(tenant)` > 0.
2. **DB Unique Index** `uq_jobs_one_active` — höchstens eine Zeile `queued`/`running`
   pro Tenant+Service (auch über Prozessgrenzen).
3. **Prozess-Mutex** um den Enqueue-Check, damit zwei Cron-Fires nicht gleichzeitig
   „frei“ sehen.
4. **Service-Gate** während `runJob` plus **Datei-Lock** `{store}/.locks/{service}.lock`.
5. **Instance-Lock** `{STORE_ROOT}/.runner.lock` — zweite Instanz startet nicht
   (sonst würde `RecoverOrphans` den DB-Lock der ersten freigeben).
6. **Delta-Tokens** werden erst gelöscht, wenn der Full-Job wirklich eingereiht wird
   (nicht schon beim Klick, falls der Dienst busy ist).
7. **Catalog Commit** serialisiert Generationen pro Tenant-Store (Datei-Lock + Job-Gate).
8. **Global** `MAX_CONCURRENT_JOBS`: begrenzt parallele Jobs **über alle Tenants**
   (Semaphore).
9. **`MAX_CONCURRENT_FULL_JOBS` (default 1):** nur so viele `job_type=full` laufen
   gleichzeitig; weitere Fulls (z. B. Empty-Store-Fan-out) warten in der Queue.
10. **Watchdog** (`JOB_STALL_TIMEOUT`, default 2h): beendet `running`-Jobs ohne
   Fortschritt (Bytes, Items, Progress-Text). Staging wird aufgeräumt; Dienst ist wieder frei.

### Verhalten bei Konflikt

| Auslöser | Reaktion |
|----------|----------|
| Cron, Service busy | Log `scheduler skip (service busy)`, kein neuer Job; `last_run` wird gesetzt |
| UI „Backup starten“, busy | Redirect `?job=busy`, Hinweis in der UI |
| Nach Admin-Consent | Nur **Exchange** wird einmal gestartet; andere Dienste folgen über Cron |

---

## Job-Lebenszyklus

```text
Enqueue ──► queued ──► (sem + service gate) ──► running
                                              │
                    ┌─────────────────────────┼─────────────────────────┐
                    ▼                         ▼                         ▼
                 success                   warning                     error
                                              │
                                         cancelled
```

Nach erfolgreichem Service-Lauf: Katalog-Snapshot (außer PST-Export) → Smart Recycle → Blob-GC.

Beim Prozessstart: `RecoverOrphans` markiert hängengebliebene `queued`/`running` als `error`.

---

## Browser / Snapshots-UI

- Snapshot-**Liste** kommt aus `catalog_snapshots` (kein Repo-Open).
- Browse/Download historischer Generationen läuft über Manifest + Blobs (**ohne** Full-Extract
  der gesamten Mailbox auf Staging).
- **Aktuell** im Dateibrowser ist der Live-Stand in `catalog_items`.

---

## Retention (Smart Recycle)

Pro Dienst: alle Versionen in den letzten *N* Stunden, danach höchstens eine pro Tag /
Woche / Monat / Jahr (+ Mindestanzahl). Unreferenzierte Blobs werden per GC gelöscht.

---

## Konfiguration (Auszug)

| Variable | Rolle |
|----------|--------|
| `MAX_CONCURRENT_JOBS` | Max. parallele Jobs global (verschiedene Tenants) |
| `MAX_CONCURRENT_FULL_JOBS` | Max. parallele Full-Syncs (Default 1) |
| `EXCHANGE_WORKERS` | Parallele Mailbox-Worker **innerhalb** eines Exchange-Jobs |
| `STORE_ROOT` | Wurzel der Tenant-Stores (Blobs/Manifeste/Exports) |

Siehe auch `.env.example` und `README.md`.
