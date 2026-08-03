-- Bind worker rollout evidence to the exact idempotent assignment operation
-- that caused it. Distinct assignments can target the same release, so the
-- desired release ID alone is not a sufficient freshness boundary.
ALTER TABLE scanner_worker_release_status
    ADD COLUMN assignment_operation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_worker_release_status
    ADD COLUMN assigned_at TIMESTAMP;
ALTER TABLE scanner_worker_release_status
    ADD COLUMN evidence_observed_at TIMESTAMP;

CREATE INDEX IF NOT EXISTS idx_scanner_worker_release_assignment
    ON scanner_worker_release_status (cohort, assignment_operation_id, last_heartbeat);
