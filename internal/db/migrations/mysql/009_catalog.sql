-- Item catalog + snapshot generations (blobs live on disk, not in SQL).

CREATE TABLE IF NOT EXISTS catalog_snapshots (
    id          VARCHAR(36) PRIMARY KEY,
    tenant_id   VARCHAR(36) NOT NULL,
    service     VARCHAR(64) NOT NULL,
    generation  INT NOT NULL,
    job_id      VARCHAR(36) NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    items_live  INT NOT NULL DEFAULT 0,
    bytes_live  BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT fk_catalog_snaps_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE,
    UNIQUE KEY uq_catalog_snaps_gen (tenant_id, service, generation),
    KEY idx_catalog_snaps_tenant (tenant_id, service, generation)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS catalog_items (
    id             VARCHAR(36) PRIMARY KEY,
    tenant_id      VARCHAR(36) NOT NULL,
    service        VARCHAR(64) NOT NULL,
    graph_item_id  TEXT NOT NULL,
    mailbox        VARCHAR(320) NOT NULL DEFAULT '',
    parent_path    TEXT NOT NULL,
    name           VARCHAR(512) NOT NULL DEFAULT '',
    blob_hash      CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
    size           BIGINT NOT NULL DEFAULT 0,
    mtime          DATETIME NULL,
    deleted        TINYINT(1) NOT NULL DEFAULT 0,
    content_type   VARCHAR(128) NOT NULL DEFAULT '',
    subject        TEXT NOT NULL DEFAULT (''),
    from_addr      VARCHAR(320) NOT NULL DEFAULT '',
    CONSTRAINT fk_catalog_items_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE,
    -- utf8mb4 unique(tenant, service, graph_item_id) exceeds InnoDB's 3072-byte key limit.
    UNIQUE KEY uq_catalog_items_graph (tenant_id, service, (SHA2(graph_item_id, 256))),
    KEY idx_catalog_items_browse (tenant_id, service, deleted, mailbox(191), parent_path(191)),
    KEY idx_catalog_items_hash (tenant_id, blob_hash)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS catalog_changes (
    id             VARCHAR(36) PRIMARY KEY,
    tenant_id      VARCHAR(36) NOT NULL,
    service        VARCHAR(64) NOT NULL,
    generation     INT NOT NULL,
    graph_item_id  TEXT NOT NULL,
    op             VARCHAR(16) NOT NULL,
    mailbox        VARCHAR(512) NOT NULL DEFAULT '',
    parent_path    VARCHAR(1024) NOT NULL DEFAULT '',
    name           VARCHAR(512) NOT NULL DEFAULT '',
    blob_hash      CHAR(64) NOT NULL DEFAULT '',
    size           BIGINT NOT NULL DEFAULT 0,
    mtime          DATETIME NULL,
    content_type   VARCHAR(128) NOT NULL DEFAULT '',
    subject        TEXT NOT NULL DEFAULT (''),
    from_addr      VARCHAR(512) NOT NULL DEFAULT '',
    CONSTRAINT fk_catalog_changes_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE,
    KEY idx_catalog_changes_gen (tenant_id, service, generation)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
