-- 025_audit_classification.sql
-- Enterprise audit classification: a semantic event type + category + severity,
-- plus request context (source IP, user agent). Empty by default for existing
-- rows; the UI falls back to method+path when unset.
ALTER TABLE audit_log ADD COLUMN event_type TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN category TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN severity TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN ip TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN user_agent TEXT NOT NULL DEFAULT '';
