CREATE TABLE IF NOT EXISTS scan_schedules (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  repo_id TEXT NOT NULL DEFAULT '',
  collection_id TEXT NOT NULL DEFAULT '',
  interval_minutes INTEGER NOT NULL,
  branch TEXT NOT NULL DEFAULT '',
  profile TEXT NOT NULL DEFAULT 'standard',
  quiet_start TEXT NOT NULL DEFAULT '',
  quiet_end TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  last_run_at DATETIME,
  last_sha TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_scan_schedules_user ON scan_schedules(user_id);
CREATE INDEX IF NOT EXISTS idx_scan_schedules_repo ON scan_schedules(repo_id);
