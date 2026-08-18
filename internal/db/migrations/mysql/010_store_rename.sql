-- Rename leftover Kopia column names to store/catalog identifiers.

ALTER TABLE tenants RENAME COLUMN kopia_password TO store_password;
ALTER TABLE tenants RENAME COLUMN kopia_repo_path TO store_path;
ALTER TABLE jobs RENAME COLUMN kopia_snapshot TO snapshot_id;
