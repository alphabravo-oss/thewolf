-- 012_remote_nodes_and_scan_targets.sql: SSH remote scan targets and scan provenance.

CREATE TABLE IF NOT EXISTS remote_nodes (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    host TEXT NOT NULL,
    port INTEGER NOT NULL DEFAULT 22,
    username TEXT NOT NULL,
    auth_type TEXT NOT NULL DEFAULT 'private_key',
    credential_secret_id TEXT REFERENCES secrets (id) ON DELETE SET NULL,
    known_hosts TEXT NOT NULL DEFAULT '',
    base_path TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    last_check_status TEXT NOT NULL DEFAULT '',
    last_check_error TEXT NOT NULL DEFAULT '',
    last_checked_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_remote_nodes_user_id ON remote_nodes (user_id);
CREATE INDEX IF NOT EXISTS idx_remote_nodes_host ON remote_nodes (host);

ALTER TABLE repos ADD COLUMN remote_node_id TEXT;
ALTER TABLE repos ADD COLUMN remote_path TEXT NOT NULL DEFAULT '';
ALTER TABLE repos ADD COLUMN last_commit_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE repos ADD COLUMN last_dirty_state TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_repos_remote_node_id ON repos (remote_node_id);

ALTER TABLE scans ADD COLUMN source_type TEXT NOT NULL DEFAULT 'local';
ALTER TABLE scans ADD COLUMN remote_node_id TEXT;
ALTER TABLE scans ADD COLUMN source_path TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN commit_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN dirty_state TEXT NOT NULL DEFAULT '';
ALTER TABLE scans ADD COLUMN prepared_workspace TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_scans_remote_node_id ON scans (remote_node_id);
CREATE INDEX IF NOT EXISTS idx_scans_commit_sha ON scans (commit_sha);
