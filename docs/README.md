# Dokumentation — Design-Intent & Abläufe

Diese Docs beschreiben **nicht** nur „wie man klickt“, sondern **was** im System passiert,
**warum** es so gebaut ist und welche Gedanken dahinterstecken.

Zielgruppe: Operatoren und Mitentwickler, die das System verstehen oder erweitern wollen.

## Lesereihenfolge

| # | Dokument | Inhalt |
|---|----------|--------|
| 1 | [architektur.md](architektur.md) | Gesamtbild, Komponenten, Datenfluss |
| 2 | [startup-konfiguration.md](startup-konfiguration.md) | Boot-Sequenz, Env-Variablen, Verdrahtung |
| 3 | [geheimnisse.md](geheimnisse.md) | MASTER_KEY vs. Store-Passwort, Crypto at rest |
| 4 | [datenbank.md](datenbank.md) | Control-Plane-Schema, Status-Werte |
| 5 | [tenants.md](tenants.md) | Anlegen → Consent → Aktiv → Löschen |
| 6 | [backup-jobs.md](backup-jobs.md) | Runner, Dienste, Progress, Orphans |
| 7 | [backup-logic.md](backup-logic.md) | Katalog, Blobs, Cron, Service-Lock |
| 8 | [speicher-katalog.md](speicher-katalog.md) | Layout, Browse, Retention, Restore |
| 9 | [api-ui.md](api-ui.md) | Auth, Routes, HTMX-Admin |
| 10 | [benachrichtigungen.md](benachrichtigungen.md) | Events, Kanäle, SSRF-Schutz |
| — | [glossar.md](glossar.md) | Begriffe kurz erklärt |

Install/Deploy-Anleitung bleibt im Root-[README.md](../README.md). Sicherheitshinweise: [SECURITY.md](../SECURITY.md).

## Leitprinzipien (Kurz)

1. **Multi-Tenant Control Plane** — viele Kunden-Tenants, ein Binary, getrennte Store-Roots.
2. **Inkrement-Ebenen** — Graph-Delta in den Katalog, Skip bereits gespeicherter Blobs, SHA-256 CAS.
3. **Offline-Recovery ohne App** — `blobs/` + `manifests/` + Store-Passwort reichen für `m365-restore`.
4. **Ein aktiver Job pro Dienst** — gleiche Services nicht parallel; unterschiedliche Dienste dürfen parallel.
5. **Secrets nie im Repo** — nur Env / verschlüsselt in der DB; siehe Workspace-Regel „No Secrets“.
