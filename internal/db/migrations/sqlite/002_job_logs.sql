-- job detail logs

CREATE TABLE IF NOT EXISTS job_logs (
    id          TEXT PRIMARY KEY,
    job_id      TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    level       TEXT NOT NULL,
    message     TEXT NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_job_logs_job ON job_logs(job_id, created_at);
