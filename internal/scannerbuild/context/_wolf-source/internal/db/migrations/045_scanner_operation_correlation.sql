-- Durable, non-secret operation correlation for scanner release workflows.
-- Correlations are separate from mutable aggregate rows so they survive
-- retries, lease transfers, archival, and worker restarts unchanged.

CREATE TABLE IF NOT EXISTS scanner_operation_correlations (
    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    trace_id TEXT NOT NULL,
    operation_id TEXT NOT NULL,
    parent_operation_id TEXT NOT NULL DEFAULT '',
    origin_component TEXT NOT NULL DEFAULT 'scanner-release',
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (aggregate_type, aggregate_id)
);

CREATE INDEX IF NOT EXISTS idx_scanner_operation_correlations_trace
    ON scanner_operation_correlations (trace_id, created_at);
CREATE INDEX IF NOT EXISTS idx_scanner_operation_correlations_operation
    ON scanner_operation_correlations (operation_id, created_at);

ALTER TABLE scanner_release_events
    ADD COLUMN trace_id TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_release_events
    ADD COLUMN operation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE scanner_release_events
    ADD COLUMN parent_operation_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_scanner_release_events_trace
    ON scanner_release_events (trace_id, created_at);
CREATE INDEX IF NOT EXISTS idx_scanner_release_events_operation
    ON scanner_release_events (operation_id, created_at);
