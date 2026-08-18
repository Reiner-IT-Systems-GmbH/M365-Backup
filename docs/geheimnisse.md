# Geheimnisse & Krypto

Es gibt **zwei getrennte Passwort-Welten**. Das verwechseln Operatoren leicht — deshalb
ausdrücklich dokumentiert.

## Die zwei Welten

| Secret | Wo | Zweck |
|--------|-----|--------|
| **`MASTER_KEY`** | nur Env | Verschlüsselt Secrets **in der App-DB** (Client Secret, Kopia-Passwort) |
| **Tenant-Kopia-Passwort** | DB (encrypted) + Offline-Recovery-Export | Öffnet das Kopia-`repo/` dieses Tenants |
| **Azure Client Secret** | DB (encrypted) | Graph App-Only Auth |
| **`ADMIN_USER`** | Env | Login-Name (Default `m365adminuser`) |
| **`ADMIN_PASSWORD`** | Env | UI-Login (bcrypt in DB); API separat per Bearer-Token |

> Recovery-Sheet in der UI: *„This is NOT the MASTER_KEY. MASTER_KEY only encrypts secrets
> in the app database.“*

### Warum getrennt?

- **Disaster Recovery ohne diese App:** Wenn die VM tot ist, brauchst du `repo/` + Kopia-Passwort
  und den stock-`kopia`-CLI. MASTER_KEY und DB sind dann egal.
- **MASTER_KEY kompromittiert ≠ Repos offen:** Angreifer mit nur dem Master-Key und der DB kann
  die Kopia-Passwörter entschlüsseln — deshalb MASTER_KEY schützen. Aber wer nur das Repo-Passwort
  hat, kann die App-DB-Secrets nicht lesen.
- **Kein Vendor-Lock:** Offline-Recovery ist ein Produktversprechen, kein Afterthought.

## Crypto-Implementierung (`internal/crypto`)

```text
Cipher = AES-256-GCM
Encrypt → base64(nonce ‖ ciphertext)
Decrypt → AEAD open
RandomPassword(n) → base64.RawURLEncoding (≥16 Bytes Entropy, sonst 32)
```

Beim Tenant-Create: `RandomPassword(32)` → verschlüsseln → als `kopia_password` speichern →
Repo damit initialisieren.

## Was nie ins Git darf

Siehe Workspace-Regel und [SECURITY.md](../SECURITY.md):

- echte `.env`, MASTER_KEY, Admin-Passwörter
- Azure Secrets, Zertifikate, Kopia-Repo-Passwörter
- Produktions-DBs und `/data/`-Repos

In Docs und `.env.example` nur Platzhalter.

## Offline-Recovery (Intent)

UI-Pfad: Tenant anlegen → Recovery zeigen → später unter Einstellungen → Offline-Recovery
(Admin-Passwort erneut eingeben → Reveal / `.txt`-Download).

Operator soll das Passwort **offline** aufbewahren (Passwort-Safe / Papier). Ohne das Passwort
sind Snapshots kryptografisch tot — das ist Absicht (Verschlüsselung at rest auf der Platte).
