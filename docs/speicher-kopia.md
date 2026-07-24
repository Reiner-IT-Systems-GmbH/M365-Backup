# Speicher & Kopia

Package: `internal/storage`. Sync/Snapshot/Lock-Logik: [backup-logic.md](backup-logic.md).

## Layout pro Tenant

```text
{KOPIA_ROOT}/{tenant-id}/
  repo/                 ← echte Kopia-Blobs (Filesystem-Backend)
  kopia.config          ← lokale Connect-Config (für CLI-Recovery nicht zwingend)
  .kopia-cache/         ← Content/Metadata-Cache (je ~100 MiB)
  sync/
    exchange/           ← Live-Mailboxen (EML)
    onedrive/           ← Live-Dateien
  exports/
    pst/{runID}/        ← EML-ZIP-Exporte
```

Kommentar in Code (`paths.go`):

> *Sibling dirs (sync/, exports/) stay outside so live sync is not mixed into blob storage.*

**Gedanke:** Live-Sync muss Dateien ändern/löschen können (Delta). Kopia-Repo ist append-/
content-addressed. Mischen würde beides erschweren und Recovery verwirren.

## Engine — wichtige Operationen

| Methode | Rolle |
|---------|--------|
| `CreateRepo` / `initializeRepo` | Repo anlegen + connecten |
| `Snapshot` | Host `m365backup`, UserName = Service, Tag `m365-service` |
| `ListSnapshots` / `ListSnapshotsCached` | Liste; Cache-TTL ~3 min |
| `ApplySmartRetention` + GC | Manifeste löschen → Maintenance |
| `Restore` / `ExportZip` | Materialisieren / ZIP |
| Virtual-FS Browse | Historische Dateien **ohne** Full-Extract |

Legacy-Repos mit altem `repo.json` (encrypted-tar-Ära) werden abgelehnt — bewusster Break,
kein stilles Datenchaos.

Reconnect: fehlt `repo/` (z. B. Volume-Wipe), wird re-initialisiert (leeres Repo, Tokens bleiben
in der DB — Operator muss Sync-Strategie kennen).

## Browse — Live vs. Historie

| Modus | Quelle | Warum |
|-------|--------|--------|
| Live | `sync/` auf Disk | Schnellster aktueller Stand |
| Snapshot | Kopia Virtual-FS | Historie ohne ganzen Baum zu extrahieren |
| Fallback Extract | Staging + `.extracted`-Marker | Nur wenn nötig |

EML-Anzeige: Subject aus Dateiname; Header-Peek begrenzt (Performance in großen Ordnern).

Snapshot-Liste gecacht, damit die Tenant-Seite nicht bei jedem Tab-Wechsel das Repo öffnet.

## Retention — Smart Recycle

Synology-ähnliches Schema **pro Service**:

| Bucket | Default |
|--------|---------|
| Hours | 24 |
| Daily | 7 |
| Weekly | 4 |
| Monthly | 6 |
| Yearly | 2 |
| KeepMin | 3 Snapshots |
| PSTKeepRuns | 5 Export-Läufe |

Ablauf: abgelaufene Manifeste löschen → Kopia Maintenance/GC gibt Blob-Platz frei.

**Gedanke:** Zeitliche Staffelung behält „gestern“ und „vor einem Jahr“, ohne jede Stunde für immer
zu speichern. Pro Service getrennt, weil Exchange-Stundenjobs sonst SharePoint-Wochenjobs
„auffressen“ würden.

## Usage / Statistik

`du`-ähnlich: Total ≈ Sync + Snapshots(`repo/`) + Exports + Other.
Cache in `tenant_usage` (stündlich + manuell).

Unterscheidung in der UI:

- **Snaps (logisch)** = Summe `TotalFileSize` der Manifeste (nicht Plattenverbrauch)
- **Snapshots (Kopia, dedup)** = echter Verbrauch von `repo/`

## Restore-Pfade

| Weg | Nutzen |
|-----|--------|
| ZIP-Download | Beliebig, offline |
| Graph-Upload (OD/SP) | Zurück nach M365 in `M365Backup-Restore/` |
| Kopia CLI | Disaster Recovery ohne diese App |

Path-Jail: `ValidateSnapshotID`, `EnsureSubpath` — kein Traversal aus Tenant-Root.

## Kapazität planen

Exchange/OneDrive halten **Live-Sync und Snapshots**. Grob:

```text
Plattenbedarf ≈ Größe(Live-Sync) + Größe(Kopia-Repo, dedup) + Exports + Cache
```

Dedup hilft bei Historie; der Live-Baum ist Klartext und „voll“.
