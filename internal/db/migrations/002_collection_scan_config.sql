-- 002_collection_scan_config.sql: Add scan configuration to collections
ALTER TABLE collections ADD COLUMN scan_config TEXT NOT NULL DEFAULT '{}';
