-- Durable scanner discovery execution. This remains additive so older
-- binaries can ignore the queue ownership and detailed result fields.

ALTER TABLE scanner_discovery_runs ADD COLUMN definition_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_discovery_runs ADD COLUMN lock_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_discovery_runs ADD COLUMN scope_json TEXT NOT NULL DEFAULT '{"mode":"complete"}';
ALTER TABLE scanner_discovery_runs ADD COLUMN coverage REAL NOT NULL DEFAULT 0;
ALTER TABLE scanner_discovery_runs ADD COLUMN total_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scanner_discovery_runs ADD COLUMN covered_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scanner_discovery_runs ADD COLUMN current_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scanner_discovery_runs ADD COLUMN unreachable_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scanner_discovery_runs ADD COLUMN unsupported_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scanner_discovery_runs ADD COLUMN held_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scanner_discovery_runs ADD COLUMN yanked_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scanner_discovery_runs ADD COLUMN unknown_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scanner_discovery_runs ADD COLUMN worker_id TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_discovery_runs ADD COLUMN lease_token TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_discovery_runs ADD COLUMN lease_expires_at TIMESTAMP;
ALTER TABLE scanner_discovery_runs ADD COLUMN heartbeat_at TIMESTAMP;
ALTER TABLE scanner_discovery_runs ADD COLUMN cancel_requested_at TIMESTAMP;
ALTER TABLE scanner_discovery_runs ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scanner_discovery_runs ADD COLUMN max_attempts INTEGER NOT NULL DEFAULT 3;
ALTER TABLE scanner_release_candidates ADD COLUMN selection_json TEXT NOT NULL DEFAULT '{}';

CREATE INDEX IF NOT EXISTS idx_scanner_discovery_claim
    ON scanner_discovery_runs (state, lease_expires_at, created_at);

ALTER TABLE scanner_update_items ADD COLUMN available_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_update_items ADD COLUMN status TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE scanner_update_items ADD COLUMN error_class TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_update_items ADD COLUMN error_detail TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_update_items ADD COLUMN resolver TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_update_items ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scanner_update_items ADD COLUMN retry_at TIMESTAMP;
ALTER TABLE scanner_update_items ADD COLUMN checked_at TIMESTAMP;
