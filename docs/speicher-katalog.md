# Speicher: Katalog + Blobs

Package: `internal/catalog` (SQL-Metadaten) + `internal/blobstore` (AES-256-GCM CAS).
Hilfen (ZIP, Pfad-Guards, EML-Meta, Usage-`du`): `internal/storage`.

## Layout pro Tenant

```text
{STORE_ROOT}/{tenant-id}/
  blobs/{hh}/{sha256}         ← verschlüsselte Content-Blobs (SHA-256 des Klartexts)
  manifests/{service}/{N}.json.zst
  exports/pst/{runID}/        ← EML-ZIP-Exporte
```

Kein `sync/`, kein `repo/`. Legacy-Reste (`repo/`, `kopia.config`, `.kopia-cache/`) werden
nach Import bzw. erstem Katalog-Snapshot gelöscht. DB-Spalten: `store_path` /
`store_password` / `jobs.snapshot_id` (Migration `010`).

## Modell

| Teil | Rolle |
|------|--------|
| **catalog_items** | Live-Stand pro Graph-Item (`deleted=0`) |
| **catalog_changes** | nur geänderte Items einer Generation |
| **catalog_snapshots** | Generation, Job, live counts |
| **blobs/** | Dedup per Hash; AES-256-GCM mit Store-Passwort |
| **manifest** | Offline-Restore ohne App/DB: `path → hash,size` |

Browse **Aktuell** = `catalog_items`. Historie = Manifest der Generation.

## Job-Lauf

Graph-Delta → `catalog.Put` / `Delete` → nach Erfolg `CommitSnapshot` + Smart Recycle + Blob-GC.
Teams/SharePoint: Full-Fetch + Reconcile (unseen → `deleted`).

## Migration bestehender Daten

Beim ersten Job nach dem Update: wenn `sync/{service}/` Dateien hat → einmal Hash/Blob/Katalog
(Generation 1), danach `sync/` löschen. **Kein Import aus `repo/`.** Ist `sync/` leer und der
Katalog leer, wird der Job als `job_type=full` eingereiht (Delta-Tokens gelöscht — kein stale
Graph-Delta). Fehlt der Katalog tenant-weit, zieht der erste Lauf alle enabled Graph-Dienste nach.

Nach erfolgreichem Import bzw. erstem Katalog-Snapshot: `repo/`, `kopia.config`, `.kopia-cache/`
werden entfernt.

## Offline-Recovery

Store-Pfad + Store-Passwort (nicht `MASTER_KEY`):

```text
m365-restore --root '{STORE_ROOT}/{tenant-id}' --password '…' \
  --service exchange --generation 1 --out /restore/target
```

Binary: `cmd/restore`.
