-- Durable, replica-safe ownership for external scanner proposal generation.

ALTER TABLE scanner_release_candidates ADD COLUMN proposal_worker_id TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_release_candidates ADD COLUMN proposal_lease_token TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_release_candidates ADD COLUMN proposal_lease_expires_at TIMESTAMP;
ALTER TABLE scanner_release_candidates ADD COLUMN proposal_heartbeat_at TIMESTAMP;
ALTER TABLE scanner_release_candidates ADD COLUMN proposal_attempt INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scanner_release_candidates ADD COLUMN proposal_max_attempts INTEGER NOT NULL DEFAULT 3;
ALTER TABLE scanner_release_candidates ADD COLUMN proposal_error_class TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_release_candidates ADD COLUMN proposal_error_detail TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_release_candidates ADD COLUMN proposal_started_at TIMESTAMP;
ALTER TABLE scanner_release_candidates ADD COLUMN proposal_completed_at TIMESTAMP;

CREATE UNIQUE INDEX IF NOT EXISTS idx_scanner_candidate_proposal_lease_token
    ON scanner_release_candidates (proposal_lease_token)
    WHERE proposal_lease_token <> '';
CREATE INDEX IF NOT EXISTS idx_scanner_candidate_proposal_claim
    ON scanner_release_candidates
       (state, proposal_lease_expires_at, proposal_attempt, created_at);
