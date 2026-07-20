-- job detail logs

CREATE TABLE IF NOT EXISTS job_logs (
    id          VARCHAR(36) PRIMARY KEY,
    job_id      VARCHAR(36) NOT NULL,
    level       VARCHAR(16) NOT NULL,
    message     TEXT NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_job_logs_job FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE,
    KEY idx_job_logs_job (job_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
