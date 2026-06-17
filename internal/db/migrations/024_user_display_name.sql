-- 024_user_display_name.sql
-- Optional human-friendly display name, shown in the UI in place of the email.
-- Empty by default; the UI falls back to the email when unset.
ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
