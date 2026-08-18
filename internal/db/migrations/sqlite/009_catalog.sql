-- Item catalog + snapshot generations (blobs live on disk, not in SQL).

CREATE TABLE IF NOT EXISTS catalog_snapshots (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    service     TEXT NOT NULL,
    generation  INTEGER NOT NULL,
    job_id      TEXT,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    items_live  INTEGER NOT NULL DEFAULT 0,
    bytes_live  INTEGER NOT NULL DEFAULT 0,
    UNIQUE(tenant_id, service, generation)
);

CREATE TABLE IF NOT EXISTS catalog_items (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    service        TEXT NOT NULL,
    graph_item_id  TEXT NOT NULL,
    mailbox        TEXT NOT NULL DEFAULT '',
    parent_path    TEXT NOT NULL DEFAULT '',
    name           TEXT NOT NULL DEFAULT '',
    blob_hash      TEXT NOT NULL DEFAULT '',
    size           INTEGER NOT NULL DEFAULT 0,
    mtime          DATETIME,
    deleted        INTEGER NOT NULL DEFAULT 0,
    content_type   TEXT NOT NULL DEFAULT '',
    subject        TEXT NOT NULL DEFAULT '',
    from_addr      TEXT NOT NULL DEFAULT '',
    UNIQUE(tenant_id, service, graph_item_id)
);

CREATE TABLE IF NOT EXISTS catalog_changes (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    service        TEXT NOT NULL,
    generation     INTEGER NOT NULL,
    graph_item_id  TEXT NOT NULL,
    op             TEXT NOT NULL,
    mailbox        TEXT NOT NULL DEFAULT '',
    parent_path    TEXT NOT NULL DEFAULT '',
    name           TEXT NOT NULL DEFAULT '',
    blob_hash      TEXT NOT NULL DEFAULT '',
    size           INTEGER NOT NULL DEFAULT 0,
    mtime          DATETIME,
    content_type   TEXT NOT NULL DEFAULT '',
    subject        TEXT NOT NULL DEFAULT '',
    from_addr      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_catalog_items_browse
    ON catalog_items(tenant_id, service, deleted, mailbox, parent_path);
CREATE INDEX IF NOT EXISTS idx_catalog_items_hash
    ON catalog_items(tenant_id, blob_hash);
CREATE INDEX IF NOT EXISTS idx_catalog_changes_gen
    ON catalog_changes(tenant_id, service, generation);
CREATE INDEX IF NOT EXISTS idx_catalog_snaps_tenant
    ON catalog_snapshots(tenant_id, service, generation);
