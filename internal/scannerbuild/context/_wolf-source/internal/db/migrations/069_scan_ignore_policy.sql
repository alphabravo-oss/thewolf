-- Empty identifies legacy scans whose historical ignore rules are unavailable.
-- Only the lease-checked capture operation writes this immutable snapshot.
ALTER TABLE scans ADD COLUMN ignore_policy_json TEXT NOT NULL DEFAULT '';

-- Legacy visibility now uses stored decisions, not today's checkout. Repair
-- headers from the same primary rows and invalidate cached gates if counts move.
UPDATE scans SET finding_count = (
 SELECT COUNT(*) FROM findings f WHERE f.scan_id=scans.id
 AND (f.cluster_id='' OR f.cluster_id=f.id) AND f.suppressed=FALSE
), findings_revision=findings_revision+1
WHERE status='completed' AND ignore_policy_json='' AND finding_count<>(
 SELECT COUNT(*) FROM findings f WHERE f.scan_id=scans.id
 AND (f.cluster_id='' OR f.cluster_id=f.id) AND f.suppressed=FALSE
);
