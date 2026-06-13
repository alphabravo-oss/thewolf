-- 017_scan_artifact_metadata.sql: artifact integrity and sensitivity metadata.

ALTER TABLE scan_artifacts ADD COLUMN checksum_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE scan_artifacts ADD COLUMN redaction_level TEXT NOT NULL DEFAULT 'internal_report';
