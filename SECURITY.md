# Security Policy

## Supported Versions

Security fixes are applied to the latest `main` branch. Tagged releases receive fixes on a best-effort basis.

## Reporting a Vulnerability

**Do not open a public issue for security vulnerabilities.**

Please report privately via:

- Email: security@example.com (replace with your project contact before publishing)
- Or your GitLab/GitHub private security advisory channel

Include:

1. Description of the issue and impact
2. Steps to reproduce (PoC without real customer data)
3. Affected version / commit if known

We aim to acknowledge reports within 5 business days.

## What Must Never Be Committed

Never commit or paste into issues/PRs:

- `.env` files or real environment values
- Azure / Entra client secrets, certificates, or private keys
- `MASTER_KEY`, admin passwords, Kopia repository passwords
- SMTP credentials or webhook URLs containing tokens
- Production databases, Kopia repos, or backup staging data
- Customer tenant IDs paired with live credentials

Use placeholders such as `<YOUR_CLIENT_SECRET>` in docs and `.env.example` only.

## Operational Hardening

- Store `MASTER_KEY` and `ADMIN_PASSWORD` only in environment variables or a secrets manager
- Restrict filesystem permissions on `DATABASE_PATH` and `KOPIA_ROOT`
- Keep Azure app registrations with least-privilege Graph permissions
- Rotate client secrets before expiry (the app notifies on upcoming expiry)
- Treat Kopia repository passwords as disaster-recovery secrets; store offline copies securely
