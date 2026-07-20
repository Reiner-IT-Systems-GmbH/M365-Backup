# Contributing

Thanks for helping improve M365 Backup.

## Ground rules

1. **Never commit secrets.** No `.env`, keys, tokens, passwords, or real Azure credentials. See [SECURITY.md](SECURITY.md) and `.gitignore`.
2. Use [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:`, `chore:`, `test:`, `refactor:`).
3. Prefer small, focused pull/merge requests.
4. Do not force-push to `main`.

## Development setup

```bash
git clone <repo-url> m365backup
cd m365backup
cp .env.example .env
# Generate MASTER_KEY: openssl rand -base64 32
# Set ADMIN_PASSWORD (12+ chars)
go mod download
go run ./cmd/server
```

Open http://localhost:8080 and sign in with `ADMIN_PASSWORD`.

## Tests

```bash
go test ./...
```

Do not put real secrets in test fixtures. Use fake UUIDs and placeholder strings such as `test-secret`.

## Secret scanning (recommended)

Before pushing, run a secret scanner locally if available, for example [gitleaks](https://github.com/gitleaks/gitleaks):

```bash
gitleaks detect --source . --verbose
```

## Code style

- Go 1.22+; format with `gofmt`
- Keep packages under `internal/` unless something must be public API
- Fail fast on missing `MASTER_KEY` / `ADMIN_PASSWORD`
- Never log decrypted secrets or tokens

## Pull / merge requests

- Describe *why* the change exists
- Link related issues (`#123`)
- Note any security or migration impact
- Update README / docs when behavior or config changes
