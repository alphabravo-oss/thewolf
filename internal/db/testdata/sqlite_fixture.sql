-- Minimal hand-written SQLite install for TestMigrateSQLiteToPostgres. It is
-- deliberately not produced by Wolf code: the migration reads whatever
-- schema an old install left behind. Only the NOT NULL columns without a
-- PostgreSQL default plus the ones the test asserts on are present.
CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT NOT NULL, password_hash TEXT NOT NULL);
CREATE TABLE repos (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, name TEXT NOT NULL, source_type TEXT NOT NULL, source_path TEXT NOT NULL);
CREATE TABLE scans (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, repo_id TEXT NOT NULL, status TEXT, tools_selected TEXT, created_at TIMESTAMP);
CREATE TABLE findings (
  id TEXT PRIMARY KEY, scan_id TEXT NOT NULL, repo_id TEXT NOT NULL, fingerprint TEXT NOT NULL, tool_name TEXT NOT NULL,
  category TEXT NOT NULL, severity TEXT NOT NULL, title TEXT NOT NULL, file_path TEXT NOT NULL, rule_id TEXT,
  status TEXT, suppressed INTEGER, corroborated_by_json TEXT, cwe_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE enterprise_records (kind TEXT NOT NULL, id TEXT NOT NULL, name TEXT, body TEXT, created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL, PRIMARY KEY (kind, id));
CREATE TABLE secrets (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, key_type TEXT NOT NULL, key_name TEXT NOT NULL, encrypted_value TEXT NOT NULL);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE removed_feature (id TEXT PRIMARY KEY); -- a table the PostgreSQL schema no longer has

INSERT INTO users VALUES ('u1', 'u1@example.test', 'hash');
INSERT INTO repos VALUES ('r1', 'u1', 'migrated', 'local', '/src');
INSERT INTO scans VALUES ('s1', 'u1', 'r1', 'running', '["semgrep","trivy"]', '2026-10-01 12:34:56.123456789+00:00');
INSERT INTO findings VALUES ('f1', 's1', 'r1', 'fp', 'semgrep', 'sast', 'high', 'placeholder', 'a.go', 'rule', 'open', 1, '["trivy"]', '');
INSERT INTO enterprise_records VALUES ('policy', 'p1', 'Policy', '{"a":1}', '2026-10-01 00:00:00+00:00', '2026-10-01 00:00:00+00:00');
INSERT INTO secrets VALUES ('sec1', 'u1', 'github', 'token', 'ciphertext-not-for-logs');
INSERT INTO settings VALUES ('fleet_mode', 'true');
INSERT INTO removed_feature VALUES ('x');
