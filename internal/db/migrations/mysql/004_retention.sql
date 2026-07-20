-- Smart Recycle retention policy (JSON) per tenant
ALTER TABLE tenants ADD COLUMN retention_json TEXT NULL;
