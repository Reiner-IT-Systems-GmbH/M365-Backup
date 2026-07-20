-- live job progress for UI
-- VARCHAR (not TEXT): MySQL disallows DEFAULT on TEXT/BLOB in non-strict/legacy modes.

ALTER TABLE jobs ADD COLUMN progress_pct INT NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN progress_message VARCHAR(1024) NOT NULL DEFAULT '';
