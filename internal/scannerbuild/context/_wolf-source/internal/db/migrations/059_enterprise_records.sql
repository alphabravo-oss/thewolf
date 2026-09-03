CREATE TABLE IF NOT EXISTS enterprise_records (
  kind TEXT NOT NULL,
  id TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '{}',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  PRIMARY KEY (kind, id)
);
