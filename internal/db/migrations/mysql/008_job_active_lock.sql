-- One queued/running job per tenant+service (DB-level, not just in-process).
-- Close leftovers first so the unique index can be created on upgrade.

UPDATE jobs
SET status = 'error',
    error_message = 'interrupted by lock-migration',
    progress_message = 'interrupted by lock-migration',
    finished_at = UTC_TIMESTAMP()
WHERE status IN ('queued', 'running');

ALTER TABLE jobs
    ADD COLUMN active_lock VARCHAR(128) AS (
        IF(status IN ('queued', 'running'), CONCAT(tenant_id, ':', service), NULL)
    ) STORED;

CREATE UNIQUE INDEX uq_jobs_one_active ON jobs (active_lock);
