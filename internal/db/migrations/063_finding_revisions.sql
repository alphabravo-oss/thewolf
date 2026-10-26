ALTER TABLE scans ADD COLUMN findings_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE quality_gate_results ADD COLUMN findings_revision BIGINT NOT NULL DEFAULT -1;
