-- One queued/running job per tenant+service (DB-level, not just in-process).
-- Close leftovers first so the unique index can be created on upgrade.

UPDATE jobs
SET status = 'error',
    error_message = 'interrupted by lock-migration',
    progress_message = 'interrupted by lock-migration',
    finished_at = CURRENT_TIMESTAMP
WHERE status IN ('queued', 'running');

CREATE UNIQUE INDEX IF NOT EXISTS uq_jobs_one_active
    ON jobs(tenant_id, service)
    WHERE status IN ('queued', 'running');
