# API & Admin-UI

Package: `internal/api` + embedded `web/` (Templates, Static, OpenAPI).

## Auth-Modell

| Aspekt | Umsetzung | Gedanke |
|--------|-----------|---------|
| Login | Ein gemeinsames `ADMIN_PASSWORD` | Early-OSS: simpel betreibbar |
| Session | Cookie 24 h, HttpOnly, SameSite=Lax | Secure wenn HTTPS / Public-URL |
| Hash | SHA-256, constant-time Compare | Kein Klartext in Session-Store |
| Rate Limit | 10 Attempts / Minute / IP | Brute-Force dämpfen |
| Öffentlich | `/login`, `/static/*`, Consent-Callback, `/healthz` | Rest hinter Session |

RBAC, 2FA, Audit-Log sind bewusst noch nicht da (Roadmap).

## Stack

- **chi** Router + RequestID / RealIP / Recoverer
- **HTMX** für Live-Job-Progress und Teil-Updates
- Templates + Static **in die Binary eingebettet** — ein Artefakt deployen

## Wichtige UI-Routen

| Route | Intent |
|-------|--------|
| `/tenants`, `/tenants/new` | Liste, Anlegen, Recovery zeigen |
| `/tenants/{id}` | Tabs: Jobs, Settings, Statistik, Snapshots, PST |
| `POST …/backup/{service}` | Job enqueuen; bei Busy Redirect `?job=busy` |
| `…/jobs/{id}/live`, Cancel | HTMX Live-Fortschritt |
| Browser / File | Live-Sync oder Snapshot-VFS |
| Restore | ZIP oder Graph-Upload |
| Recovery | Re-Auth → Kopia-Passwort reveal/download |
| `/settings` | Notification-Kanäle |
| `/openapi` | Spec / Swagger |

## JSON-API

Spiegel der UI-Funktionen unter `/api/…` (Tenants, Jobs, Schedules, Restore, Notifications,
Consent, Usage-Refresh).

**Redaction:** `publicTenant` und Notification-Config strippen Secrets — API antwortet nie mit
Client Secret oder Klartext-Kopia-Passwort.

## Consent-Callback

Öffentlich erreichbar (Entra redirected den Browser), aber durch HMAC-State geschützt
(siehe [tenants.md](tenants.md)).

## Design-UI

Admin-Oberfläche ist funktional (Ops-Tool), kein Marketing-Landing. Bestehende Patterns
(Tabs, Live-Jobs, Browser) beibehalten — keine generische Dashboard-Spielerei.
