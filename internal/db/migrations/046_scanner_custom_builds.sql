-- Durable compatibility operations for administrator-requested scanner image
-- builds. Requests describe only compiled scanner variants and supported
-- platforms. No Dockerfile, context path, or command is persisted.

CREATE TABLE IF NOT EXISTS scanner_custom_builds (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    variants_json TEXT NOT NULL,
    push BOOLEAN NOT NULL DEFAULT FALSE,
    platforms_json TEXT NOT NULL DEFAULT '[]',
    namespace TEXT NOT NULL,
    reserved_version TEXT NOT NULL,
    publish_version TEXT UNIQUE,
    secret_reference TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL CHECK (state IN ('queued', 'claimed', 'running', 'completed', 'partial', 'failed', 'cancelled')),
    actor TEXT NOT NULL,
    reason TEXT NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,
    request_digest TEXT NOT NULL,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_token TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMP,
    heartbeat_at TIMESTAMP,
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    max_attempts INTEGER NOT NULL DEFAULT 3 CHECK (max_attempts > 0),
    available_at TIMESTAMP NOT NULL,
    cancel_requested_at TIMESTAMP,
    error_class TEXT NOT NULL DEFAULT '',
    error_detail TEXT NOT NULL DEFAULT '',
    summary_json TEXT NOT NULL DEFAULT '{}',
    version BIGINT NOT NULL DEFAULT 1,
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_scanner_custom_build_claim
    ON scanner_custom_builds (state, available_at, lease_expires_at, created_at);
CREATE INDEX IF NOT EXISTS idx_scanner_custom_build_user
    ON scanner_custom_builds (user_id, created_at, id);

CREATE TABLE IF NOT EXISTS scanner_custom_build_variants (
    id TEXT PRIMARY KEY,
    build_id TEXT NOT NULL REFERENCES scanner_custom_builds (id) ON DELETE CASCADE,
    variant TEXT NOT NULL CHECK (variant IN ('default', 'jvm', 'rust', 'codeql')),
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'completed', 'failed', 'cancelled')),
    refs_json TEXT NOT NULL DEFAULT '[]',
    digest TEXT NOT NULL DEFAULT '',
    loaded_locally BOOLEAN NOT NULL DEFAULT FALSE,
    pushed BOOLEAN NOT NULL DEFAULT FALSE,
    error_class TEXT NOT NULL DEFAULT '',
    error_detail TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1,
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE (build_id, variant),
    UNIQUE (build_id, ordinal)
);

CREATE INDEX IF NOT EXISTS idx_scanner_custom_build_variant_state
    ON scanner_custom_build_variants (build_id, state, ordinal);

CREATE TABLE IF NOT EXISTS scanner_custom_build_logs (
    build_id TEXT NOT NULL REFERENCES scanner_custom_builds (id) ON DELETE CASCADE,
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    variant TEXT NOT NULL CHECK (variant IN ('default', 'jvm', 'rust', 'codeql')),
    line TEXT NOT NULL CHECK (LENGTH(line) <= 8192),
    redacted BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (build_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_scanner_custom_build_logs_variant
    ON scanner_custom_build_logs (build_id, variant, sequence);
