-- Durable ownership for scanner rollout reconciliation. Claims remain in a
-- separate table so controller lease churn does not mutate the rollout's
-- optimistic-concurrency version or business state.
CREATE TABLE IF NOT EXISTS scanner_rollout_claims (
    rollout_id TEXT PRIMARY KEY REFERENCES scanner_rollouts (id) ON DELETE CASCADE,
    worker_id TEXT NOT NULL,
    lease_token TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('active', 'released')),
    lease_expires_at TIMESTAMP NOT NULL,
    heartbeat_at TIMESTAMP NOT NULL,
    available_at TIMESTAMP NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 1 CHECK (attempt > 0),
    version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_scanner_rollout_claim_token
    ON scanner_rollout_claims (lease_token);
CREATE INDEX IF NOT EXISTS idx_scanner_rollout_claim_available
    ON scanner_rollout_claims (state, available_at, lease_expires_at);

-- Cohort lifecycle timestamps make minimum observation periods and stuck
-- assignment deadlines resumable after controller restarts.
ALTER TABLE scanner_rollout_cohorts ADD COLUMN started_at TIMESTAMP;
ALTER TABLE scanner_rollout_cohorts ADD COLUMN health_observed_at TIMESTAMP;
ALTER TABLE scanner_rollout_cohorts ADD COLUMN completed_at TIMESTAMP;
