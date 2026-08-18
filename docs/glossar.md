# Glossar

| Begriff | Bedeutung |
|---------|-----------|
| **Aktuell** | Live-Stand in `catalog_items` (`deleted=0`) |
| **Snapshot / Generation** | Punkt-in-Zeit im Katalog + Manifest auf Disk |
| **Blob** | AES-256-GCM CAS-Objekt, Hash = SHA-256 des Klartexts |
| **Manifest** | `{service}/{generation}.json.zst` für Offline-Restore |
| **Store-Root** | `{STORE_ROOT}/{tenant-id}/` |
| **Store-Passwort** | Öffnet Blobs/Manifeste; DB-Spalte `store_password` |
| **MASTER_KEY** | Env-Key zum Verschlüsseln von DB-Secrets — **nicht** der Store-Key |
| **SkipSnapshot** | Runner schreibt keine Katalog-Generation (PST-Export) |
| **Smart Recycle** | Retention pro Dienst (Stunden/Tage/Wochen/Monate/Jahre) |
| **GC** | Unreferenzierte Blobs löschen |
