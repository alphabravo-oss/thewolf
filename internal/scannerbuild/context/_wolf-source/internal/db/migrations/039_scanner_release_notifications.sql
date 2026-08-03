-- Transactional scanner-release notification outbox. External destinations
-- are opaque policy references. Endpoint addresses and credentials remain in
-- the adapter's deployment configuration.

CREATE TABLE IF NOT EXISTS scanner_release_notifications (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES scanner_release_events (id) ON DELETE RESTRICT,
    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    notification_type TEXT NOT NULL,
    destination_type TEXT NOT NULL CHECK (destination_type IN ('ui', 'webhook', 'email', 'siem')),
    destination_ref TEXT NOT NULL,
    policy_id TEXT NOT NULL DEFAULT '',
    policy_revision BIGINT NOT NULL DEFAULT 0,
    state TEXT NOT NULL CHECK (state IN ('pending', 'delivering', 'retry', 'delivered', 'dead_letter')),
    payload_json TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    max_attempts INTEGER NOT NULL DEFAULT 8 CHECK (max_attempts > 0),
    available_at TIMESTAMP NOT NULL,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_token TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMP,
    heartbeat_at TIMESTAMP,
    delivered_at TIMESTAMP,
    dead_lettered_at TIMESTAMP,
    error_class TEXT NOT NULL DEFAULT '',
    error_detail TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE (event_id, destination_type, destination_ref)
);

CREATE INDEX IF NOT EXISTS idx_scanner_release_notification_queue
    ON scanner_release_notifications (state, available_at, created_at);
CREATE INDEX IF NOT EXISTS idx_scanner_release_notification_lease
    ON scanner_release_notifications (state, lease_expires_at);
CREATE INDEX IF NOT EXISTS idx_scanner_release_notification_event
    ON scanner_release_notifications (event_id, destination_type);
CREATE INDEX IF NOT EXISTS idx_scanner_release_notification_dead_letter
    ON scanner_release_notifications (state, dead_lettered_at);
