-- M365 Backup schema v1 (MySQL 8+ / MariaDB 10.6+)

CREATE TABLE IF NOT EXISTS tenants (
    id              VARCHAR(36) PRIMARY KEY,
    name            VARCHAR(255) NOT NULL,
    azure_tenant_id VARCHAR(64) NOT NULL,
    client_id       VARCHAR(64) NOT NULL,
    client_secret   TEXT NOT NULL,
    secret_expires  DATETIME NULL,
    kopia_password  TEXT NOT NULL,
    kopia_repo_path VARCHAR(512) NOT NULL,
    status          VARCHAR(32) NOT NULL DEFAULT 'setup',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uq_tenants_azure (azure_tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS schedules (
    id          VARCHAR(36) PRIMARY KEY,
    tenant_id   VARCHAR(36) NOT NULL,
    service     VARCHAR(64) NOT NULL,
    cron_expr   VARCHAR(64) NOT NULL,
    enabled     TINYINT(1) NOT NULL DEFAULT 1,
    last_run    DATETIME NULL,
    next_run    DATETIME NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_schedules_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE,
    KEY idx_schedules_tenant (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS jobs (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         VARCHAR(36) NOT NULL,
    schedule_id       VARCHAR(36) NULL,
    service           VARCHAR(64) NOT NULL,
    job_type          VARCHAR(32) NOT NULL,
    status            VARCHAR(32) NOT NULL,
    started_at        DATETIME NULL,
    finished_at       DATETIME NULL,
    items_new         INT NOT NULL DEFAULT 0,
    items_total       INT NOT NULL DEFAULT 0,
    bytes_transferred BIGINT NOT NULL DEFAULT 0,
    error_message     TEXT NULL,
    kopia_snapshot    VARCHAR(128) NULL,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_jobs_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE,
    CONSTRAINT fk_jobs_schedule FOREIGN KEY (schedule_id) REFERENCES schedules(id) ON DELETE SET NULL,
    KEY idx_jobs_tenant (tenant_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS delta_tokens (
    id          VARCHAR(36) PRIMARY KEY,
    tenant_id   VARCHAR(36) NOT NULL,
    service     VARCHAR(64) NOT NULL,
    user_id     VARCHAR(255) NOT NULL,
    token       TEXT NOT NULL,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT fk_delta_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE,
    UNIQUE KEY uq_delta (tenant_id, service, user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS notification_settings (
    id          VARCHAR(36) PRIMARY KEY,
    tenant_id   VARCHAR(36) NULL,
    channel     VARCHAR(32) NOT NULL,
    enabled     TINYINT(1) NOT NULL DEFAULT 1,
    config      TEXT NOT NULL,
    notify_on   TEXT NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_notify_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS notification_log (
    id          VARCHAR(36) PRIMARY KEY,
    tenant_id   VARCHAR(36) NULL,
    channel     VARCHAR(32) NOT NULL,
    event_type  VARCHAR(64) NOT NULL,
    subject     VARCHAR(512) NOT NULL,
    sent_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    success     TINYINT(1) NOT NULL,
    error       TEXT NULL,
    CONSTRAINT fk_notify_log_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
