-- M365 Backup schema v1

CREATE TABLE IF NOT EXISTS tenants (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    azure_tenant_id TEXT NOT NULL UNIQUE,
    client_id       TEXT NOT NULL,
    client_secret   TEXT NOT NULL,
    secret_expires  DATETIME,
    kopia_password  TEXT NOT NULL,
    kopia_repo_path TEXT NOT NULL,
    status          TEXT DEFAULT 'setup',
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS schedules (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    service     TEXT NOT NULL,
    cron_expr   TEXT NOT NULL,
    enabled     INTEGER DEFAULT 1,
    last_run    DATETIME,
    next_run    DATETIME,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS jobs (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    schedule_id       TEXT REFERENCES schedules(id) ON DELETE SET NULL,
    service           TEXT NOT NULL,
    job_type          TEXT NOT NULL,
    status            TEXT NOT NULL,
    started_at        DATETIME,
    finished_at       DATETIME,
    items_new         INTEGER DEFAULT 0,
    items_total       INTEGER DEFAULT 0,
    bytes_transferred INTEGER DEFAULT 0,
    error_message     TEXT,
    kopia_snapshot    TEXT,
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS delta_tokens (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    service     TEXT NOT NULL,
    user_id     TEXT NOT NULL,
    token       TEXT NOT NULL,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, service, user_id)
);

CREATE TABLE IF NOT EXISTS notification_settings (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT REFERENCES tenants(id) ON DELETE CASCADE,
    channel     TEXT NOT NULL,
    enabled     INTEGER DEFAULT 1,
    config      TEXT NOT NULL,
    notify_on   TEXT NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS notification_log (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT REFERENCES tenants(id) ON DELETE SET NULL,
    channel     TEXT NOT NULL,
    event_type  TEXT NOT NULL,
    subject     TEXT NOT NULL,
    sent_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
    success     INTEGER NOT NULL,
    error       TEXT
);

CREATE TABLE IF NOT EXISTS schema_migrations (
    version     INTEGER PRIMARY KEY,
    applied_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_jobs_tenant ON jobs(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_schedules_tenant ON schedules(tenant_id);
CREATE INDEX IF NOT EXISTS idx_delta_tokens_lookup ON delta_tokens(tenant_id, service, user_id);
