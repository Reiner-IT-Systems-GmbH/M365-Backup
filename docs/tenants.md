# Tenant-Lebenszyklus

Package: `internal/tenant`.

## Ablauf

```text
Create (UI)
  │  Client Secret encrypten
  │  Kopia-Passwort generieren + encrypten
  │  DB-Row status=setup
  │  Repo unter {KOPIA_ROOT}/{id}/ anlegen
  │  Default-Schedules einfügen
  │  Recovery-Passwort einmal zeigen
  ▼
Admin Consent (Entra)
  │  ConsentURL mit HMAC-State (TTL 1h)
  │  Callback /api/consent/callback
  │  Activate → status=active
  │  Nur Exchange einmal enqueuen (full)
  ▼
Betrieb
  │  Cron für alle enabled Schedules
  │  Keycheck täglich (Secret-Ablauf)
  │  Usage-Scan stündlich
  ▼
Delete (optional)
     Nur DB-Row — On-Disk-Repo bleibt (Operator räumt auf)
```

## Create — was und warum

1. **Eigenes Kopia-Passwort pro Tenant** — Isolation: ein geleakter Tenant-Key öffnet nicht alle Repos.
2. **Repo sofort anlegen** — Consent kann dauern; Speicherpfad und Offline-Recovery stehen früh fest.
3. **Status `setup`** — kein Backup bevor Admin Consent durch ist (sonst Graph-Fehler-Spam).
4. **Default-Schedules** — Zero-Config für neue Tenants; gestaffelte Crons (siehe backup-logic).

## Consent

- URL: `login.microsoftonline.com/{azureTenant}/adminconsent`
- `redirect_uri` = `{PUBLIC_BASE_URL}/api/consent/callback`
- State: HMAC-SHA256 über Master-Key-Material, Payload `{t,e}`, TTL 1 Stunde
- Button bleibt auch bei Status `active` sichtbar (Permissions nachziehen)

**Gedanke hinter HMAC-State:** Callback darf nicht mit fremder `tenant_id` aktiviert werden.
Ohne gültiges State → kein Activate.

Nach **erstem** erfolgreichem Consent:

> *Start only Exchange first — other services follow via cron (and may run in parallel with Exchange).*

Warum nur Exchange? Der erste Full-Sync ist oft der schwerste. Andere Dienste starten über ihre
Default-Crons und blockieren sich untereinander nicht mehr über einen Tenant-weiten Lock.

**Re-Consent** (Tenant schon `active`): nur Redirect mit Flash — kein erneutes Activate, kein Job-Enqueue.
Nützlich nach nachträglich ergänzten Graph-Permissions (z. B. `Channel.ReadBasic.All`).

## Keycheck

Täglich ~08:00. Schwellen: abgelaufen / ≤7 Tage / ≤30 Tage → Notification-Events.
**Keine Auto-Rotation** — Secrets rotieren ist ein bewusster Admin-Schritt in Azure + UI-Update.

## Delete

Nur `DeleteTenant` in der DB. Das Repo auf Disk bleibt liegen.

**Gedanke:** Versehentliches UI-Löschen soll keine Kundendaten vernichten. Aufräumen ist
Operator-Aufgabe (Volume / `rm` nach Bestätigung).
