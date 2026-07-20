-- Cached disk usage (du-style) per tenant, refreshed by cron or admin trigger
CREATE TABLE IF NOT EXISTS tenant_usage (
    tenant_id    VARCHAR(36) PRIMARY KEY,
    total_bytes  BIGINT NOT NULL DEFAULT 0,
    report_json  MEDIUMTEXT NOT NULL,
    measured_at  DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL,
    CONSTRAINT fk_tenant_usage_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
