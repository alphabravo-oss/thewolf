-- 003_repo_detection_cache.sql: Cache detection results on repos
ALTER TABLE repos ADD COLUMN detected_languages TEXT NOT NULL DEFAULT '{}';
ALTER TABLE repos ADD COLUMN detected_frameworks TEXT NOT NULL DEFAULT '[]';
ALTER TABLE repos ADD COLUMN detected_at TIMESTAMP;
