-- optional JSON params for jobs (e.g. PST export scope)
-- MySQL rejects DEFAULT on TEXT/BLOB/JSON. Reads use COALESCE(params, '').

ALTER TABLE jobs ADD COLUMN params TEXT NULL;
