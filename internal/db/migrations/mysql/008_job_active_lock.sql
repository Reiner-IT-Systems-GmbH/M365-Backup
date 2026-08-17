-- One queued/running job per tenant+service (DB-level, not just in-process).
-- Close leftovers first so the unique index can be created on upgrade.
--
-- Do not use a STORED generated column here: jobs.tenant_id is a foreign key,
-- and MySQL 8.x then fails ALTER TABLE with Error 1215 (HY000).
-- A functional unique index keeps the same "NULL when not active" semantics.

UPDATE jobs
SET status = 'error',
    error_message = 'interrupted by lock-migration',
    progress_message = 'interrupted by lock-migration',
    finished_at = UTC_TIMESTAMP()
WHERE status IN ('queued', 'running');

CREATE UNIQUE INDEX uq_jobs_one_active ON jobs ((
    CAST(IF(status IN ('queued', 'running'), CONCAT(tenant_id, ':', service), NULL) AS CHAR(128))
));
