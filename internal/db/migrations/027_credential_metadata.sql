-- 027_credential_metadata.sql
-- Host binding and non-secret metadata for API-managed source credentials.

ALTER TABLE secrets ADD COLUMN allowed_hosts TEXT NOT NULL DEFAULT '[]';
ALTER TABLE secrets ADD COLUMN metadata_json TEXT NOT NULL DEFAULT '{}';
