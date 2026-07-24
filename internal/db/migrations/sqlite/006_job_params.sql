-- optional JSON params for jobs (e.g. PST export scope)

ALTER TABLE jobs ADD COLUMN params TEXT NOT NULL DEFAULT '';
