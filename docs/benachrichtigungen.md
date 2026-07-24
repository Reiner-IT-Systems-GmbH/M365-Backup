# Benachrichtigungen

Package: `internal/notification`.

## Events

| Event | Wann |
|-------|------|
| `job_error` | Job hart fehlgeschlagen |
| `job_warning` | Job mit Warnings durchgelaufen |
| `job_success` | Erfolg (optional, oft laut) |
| `key_expiry_30d` / `_7d` / `key_expired` | Azure Client Secret |
| `quota_warning` | (vorbereitet / Usage-bezogen) |
| `restore_done` | Restore abgeschlossen |

## Ablauf

```text
Send(event)
  → notification_settings laden (global und/oder tenant-scoped)
  → Event in notify_on-Array?
  → Kanal: smtp | webhook/slack/teams | pushover
  → Eintrag in notification_log
  → Fallback: Env-SMTP nur für job_error und key_*, wenn nichts matched
```

**Gedanke Fallback-SMTP:** Frische Installationen sollen kritische Fehler melden können,
bevor jemand die Settings-UI konfiguriert hat — aber nicht jedes Success-Mail fluten.

## Kanäle

- **SMTP** — aus Settings oder Env (`SMTP_*`)
- **Webhook / Slack / Teams** — generische HTTP-POSTs
- **Pushover** — inkl. Priority/Sounds/Emergency-Optionen (Uptime-Kuma-ähnlich)

## SSRF-Schutz

Webhooks dürfen nicht ungefiltert in Cloud-Metadaten oder Link-Local schießen.

| Blockiert | Erlaubt (bewusst) |
|-----------|-------------------|
| Cloud-Metadata-Hosts | Private RFC1918 |
| `169.254.0.0/16` | Loopback |
| AWS IMDS IPv6 | Self-hosted Slack/n8n im LAN |

Zusätzlich Dial-Time-Check gegen DNS-Rebinding.

**Gedanke:** Strenges „nur Public-Internet“ würde typische Self-Hosted-Setups (Webhook im LAN)
brechen. Deshalb: gefährliche Cloud-Metadaten blocken, private Netze erlauben.
