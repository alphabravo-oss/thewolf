CREATE TABLE IF NOT EXISTS vulnerabilities (
  id TEXT PRIMARY KEY,
  repo_id TEXT NOT NULL,
  scan_id TEXT NOT NULL DEFAULT '',
  canonical_key TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  severity TEXT NOT NULL DEFAULT '',
  category TEXT NOT NULL DEFAULT '',
  fine_category TEXT NOT NULL DEFAULT '',
  confidence TEXT NOT NULL DEFAULT '',
  baseline_state TEXT NOT NULL DEFAULT '',
  composite_score REAL NOT NULL DEFAULT 0,
  evidence_count INTEGER NOT NULL DEFAULT 1,
  finding_ids_json TEXT NOT NULL DEFAULT '[]',
  corroborated_by_json TEXT NOT NULL DEFAULT '[]',
  suppressed INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE(repo_id, canonical_key)
);
CREATE INDEX IF NOT EXISTS idx_vulnerabilities_scan ON vulnerabilities(scan_id);
CREATE INDEX IF NOT EXISTS idx_vulnerabilities_repo ON vulnerabilities(repo_id);
