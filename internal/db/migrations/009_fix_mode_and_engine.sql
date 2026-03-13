-- Add mode and engine columns to fixes table for Wolf Pack mode support.
ALTER TABLE fixes ADD COLUMN mode TEXT NOT NULL DEFAULT 'interactive';
ALTER TABLE fixes ADD COLUMN engine TEXT NOT NULL DEFAULT '';
