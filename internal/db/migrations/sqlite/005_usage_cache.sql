-- Cached disk usage (du-style) per tenant, refreshed by cron or admin trigger
CREATE TABLE IF NOT EXISTS tenant_usage (
    tenant_id    TEXT PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    total_bytes  INTEGER NOT NULL DEFAULT 0,
    report_json  TEXT NOT NULL,
    measured_at  DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL
);
