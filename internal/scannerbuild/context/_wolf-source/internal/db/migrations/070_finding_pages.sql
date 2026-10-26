-- Cover the default scan list order and visible/hidden counts without reading
-- native report blobs. ID breaks ties consistently across both stores.
CREATE INDEX IF NOT EXISTS idx_findings_scan_visible_severity ON findings (
    scan_id, suppressed,
    (CASE severity WHEN 'critical' THEN 5 WHEN 'high' THEN 4 WHEN 'medium' THEN 3 WHEN 'low' THEN 2 WHEN 'info' THEN 1 ELSE 0 END) DESC,
    id
) WHERE cluster_id = '' OR cluster_id = id;
