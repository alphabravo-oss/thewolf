ALTER TABLE scanner_registry_targets ADD COLUMN health_status TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE scanner_registry_targets ADD COLUMN last_checked_at TIMESTAMP;
ALTER TABLE scanner_registry_targets ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_registry_targets ADD COLUMN latency_ms BIGINT NOT NULL DEFAULT 0;
ALTER TABLE scanner_registry_targets ADD COLUMN digest_parity_status TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE scanner_registry_targets ADD COLUMN mirror_lag_seconds BIGINT NOT NULL DEFAULT 0;
ALTER TABLE scanner_registry_targets ADD COLUMN health_detail_json TEXT NOT NULL DEFAULT '{}';
