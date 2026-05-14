-- 009_scan_tools_errors.sql: per-tool failure messages
--
-- Previously the scan record only stored which tools failed (tools_failed,
-- a JSON array of names). The UI needs the actual error message so an
-- operator can understand *why* a tool failed. Add a tools_errors column
-- that holds a JSON object of {toolName: errorMessage}.
ALTER TABLE scans ADD COLUMN tools_errors TEXT NOT NULL DEFAULT '{}';
