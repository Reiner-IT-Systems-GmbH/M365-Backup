# Glossar

| Begriff | Kurz |
|---------|------|
| **Control Plane** | App + DB: Tenants, Jobs, Tokens, UI — nicht die Backup-Bytes |
| **Live-Sync** | Persistenter Klartext-Baum unter `sync/` für Graph-Delta |
| **Snapshot** | Verschlüsselter Punkt-in-Zeit in Kopia `repo/` |
| **Staging** | Ephemeres Job-Verzeichnis unter `STAGING_ROOT` |
| **Tenant** | Ein Entra-/M365-Kundenmandant in dieser App |
| **Service** | `exchange`, `onedrive`, `teams`, `sharepoint`, `pst` |
| **Job** | Ein einzelner Backup-/Export-Lauf eines Service |
| **Schedule** | Cron-Zeile pro Tenant+Service |
| **Delta-Token** | Graph-Fortsetzungslink in der DB |
| **Service-Lock** | Höchstens ein aktiver Job (`queued`/`running`) pro Tenant+Service; andere Dienste parallel |
| **Smart Recycle** | Synology-ähnliche Retention (h/d/w/m/y + KeepMin) |
| **MASTER_KEY** | Env-Key zum Verschlüsseln von DB-Secrets — **nicht** Kopia |
| **Kopia-Passwort** | Öffnet das Tenant-`repo/`; Basis für Offline-Recovery |
| **Consent** | Entra Admin-Zustimmung für die App-Registration |
| **Orphan** | Job, der nach Prozess-Crash noch `queued`/`running` war |
| **SkipSnapshot** | Runner macht keinen Kopia-Lauf (PST-Export) |
| **Warning-Status** | Job durch, aber mit gemeldeten Warnings |
| **Snaps (logisch)** | Summe Manifest-Dateigrößen — nicht Disk-Verbrauch |
| **GC / Maintenance** | Kopia gibt nach Manifest-Löschung Blob-Platz frei |

Siehe auch den Einstieg: [README.md](README.md).
