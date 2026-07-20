-- live job progress for UI

ALTER TABLE jobs ADD COLUMN progress_pct INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN progress_message TEXT NOT NULL DEFAULT '';
