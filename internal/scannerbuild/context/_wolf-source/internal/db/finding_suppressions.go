package db

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/alphabravocompany/thewolf/internal/finding/identity"
	scansuppress "github.com/alphabravocompany/thewolf/internal/scan/suppress"
	"sort"
	"time"

	"github.com/alphabravocompany/thewolf/internal/finding/suppression"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func createFindingSuppression(ctx context.Context, database *sqlx.DB, rule *models.FindingSuppression) error {
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockVulnerabilityRepo(ctx, tx, rule.RepoID); err != nil {
		return err
	}
	now := time.Now().UTC()
	rule.CreatedAt, rule.UpdatedAt = now, now
	if rule.Status == "" {
		rule.Status = models.SuppressionStatusActive
	}
	if _, err := tx.NamedExecContext(ctx, `INSERT INTO finding_suppressions
 (id,repo_id,created_by,scope_type,scope_value,branch,reason,expires_at,status,created_at,updated_at)
 VALUES (:id,:repo_id,:created_by,:scope_type,:scope_value,:branch,:reason,:expires_at,:status,:created_at,:updated_at)`, rule); err != nil {
		return err
	}
	if err := reconcileFindingSuppressionsTx(ctx, tx, rule.RepoID, now); err != nil {
		return err
	}
	if err := appendSuppressionAuditTx(ctx, tx, rule.ID, "created", rule.CreatedBy, now); err != nil {
		return err
	}
	return tx.Commit()
}

func revokeFindingSuppression(ctx context.Context, database *sqlx.DB, id, actorID string) error {
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var repoID string
	if err := tx.GetContext(ctx, &repoID, tx.Rebind("SELECT repo_id FROM finding_suppressions WHERE id = ?"), id); err != nil {
		return err
	}
	if err := lockVulnerabilityRepo(ctx, tx, repoID); err != nil {
		return err
	}
	var state models.SuppressionStatus
	if err := tx.GetContext(ctx, &state, tx.Rebind("SELECT status FROM finding_suppressions WHERE id = ?"), id); err != nil {
		return err
	}
	if state == models.SuppressionStatusRevoked {
		return tx.Commit()
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, tx.Rebind("UPDATE finding_suppressions SET status = ?, updated_at = ? WHERE id = ?"), models.SuppressionStatusRevoked, now, id); err != nil {
		return err
	}
	if err := reconcileFindingSuppressionsTx(ctx, tx, repoID, now); err != nil {
		return err
	}
	if err := appendSuppressionAuditTx(ctx, tx, id, "revoked", actorID, now); err != nil {
		return err
	}
	return tx.Commit()
}

// Expiration is restart-safe and idempotent across API replicas. Each repository
// commits its rule status, flags, revisions, counts and audit together.
func expireFindingSuppressions(ctx context.Context, database *sqlx.DB, now time.Time) (int, error) {
	var repos []string
	if err := database.SelectContext(ctx, &repos, database.Rebind("SELECT DISTINCT repo_id FROM finding_suppressions WHERE status = ? AND expires_at <= ? ORDER BY repo_id"), models.SuppressionStatusActive, now); err != nil {
		return 0, err
	}
	count := 0
	for _, repoID := range repos {
		n, err := expireRepoSuppressions(ctx, database, repoID, now)
		if err != nil {
			return count, err
		}
		count += n
	}
	return count, nil
}
func expireRepoSuppressions(ctx context.Context, database *sqlx.DB, repoID string, now time.Time) (int, error) {
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockVulnerabilityRepo(ctx, tx, repoID); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	var ids []string
	if err := tx.SelectContext(ctx, &ids, tx.Rebind("SELECT id FROM finding_suppressions WHERE repo_id = ? AND status = ? AND expires_at <= ? ORDER BY id"), repoID, models.SuppressionStatusActive, now); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if _, err := tx.ExecContext(ctx, tx.Rebind("UPDATE finding_suppressions SET status = ?, updated_at = ? WHERE repo_id = ? AND status = ? AND expires_at <= ?"), models.SuppressionStatusExpired, now, repoID, models.SuppressionStatusActive, now); err != nil {
		return 0, err
	}
	if err := reconcileFindingSuppressionsTx(ctx, tx, repoID, now); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := appendSuppressionAuditTx(ctx, tx, id, "expired", "", now); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func appendSuppressionAuditTx(ctx context.Context, tx *sqlx.Tx, id, action, actor string, now time.Time) error {
	_, err := tx.ExecContext(ctx, tx.Rebind("INSERT INTO finding_suppression_audit (id,suppression_id,action,actor_id,details_json,created_at) VALUES (?,?,?,?,?,?)"), uuid.NewString(), id, action, actor, "{}", now)
	return err
}

func loadFindingSuppressions(ctx context.Context, tx *sqlx.Tx, repoID string) ([]models.FindingSuppression, error) {
	var rules []models.FindingSuppression
	err := tx.SelectContext(ctx, &rules, tx.Rebind("SELECT * FROM finding_suppressions WHERE repo_id = ? ORDER BY created_at DESC, id"), repoID)
	return rules, err
}

// Callers hold the repo lock. Scan locks use the same order as canonical and
// baseline updates. Only suppression columns change, preserving concurrent triage.
// ponytail: reconciliation visits all repository observations on a rule mutation;
// use measured fleet limits to choose indexed scope queries before widening scale.
func reconcileFindingSuppressionsTx(ctx context.Context, tx *sqlx.Tx, repoID string, now time.Time) error {
	rules, err := loadFindingSuppressions(ctx, tx, repoID)
	if err != nil {
		return err
	}
	var scans []models.Scan
	if err := tx.SelectContext(ctx, &scans, tx.Rebind("SELECT * FROM scans WHERE repo_id = ? ORDER BY id"), repoID); err != nil {
		return err
	}
	// Lock all observations before history derivation can read an earlier scan.
	for i, scan := range scans {
		current, err := lockClusterScan(ctx, tx, repoID, scan.ID)
		if err != nil {
			return err
		}
		scans[i] = *current
	}
	sort.Slice(scans, func(i, j int) bool {
		a, b := scans[i], scans[j]
		if a.BaselineResolvedAt != nil && b.BaselineResolvedAt != nil && !a.BaselineResolvedAt.Equal(*b.BaselineResolvedAt) {
			return a.BaselineResolvedAt.Before(*b.BaselineResolvedAt)
		}
		if (a.BaselineResolvedAt == nil) != (b.BaselineResolvedAt == nil) {
			return a.BaselineResolvedAt == nil
		}
		return a.ID < b.ID
	})
	for _, scan := range scans {
		var findings []models.Finding
		if err := tx.SelectContext(ctx, &findings, tx.Rebind("SELECT * FROM findings WHERE scan_id = ? AND repo_id = ? ORDER BY id"), scan.ID, repoID); err != nil {
			return err
		}
		hydrateFindingsAfterRead(findings)
		projected := append([]models.Finding(nil), findings...)
		suppression.Apply(projected, rules, map[string]string{scan.ID: scan.Branch}, now)
		if scan.IgnorePolicyJSON != "" {
			if _, _, err := scansuppress.ApplyScan(ctx, projected, &scan, ""); err != nil {
				return err
			}
		}
		changed := false
		for i, before := range findings {
			f := projected[i]
			findings[i] = f
			if f.Suppressed == before.Suppressed && f.SuppressionID == before.SuppressionID && f.SuppressedReason == before.SuppressedReason {
				continue
			}
			if _, err := tx.ExecContext(ctx, tx.Rebind("UPDATE findings SET suppressed = ?, suppression_id = ?, suppressed_reason = ?, updated_at = ? WHERE id = ?"), f.Suppressed, f.SuppressionID, f.SuppressedReason, now, f.ID); err != nil {
				return err
			}
			changed = true
		}
		promoted, err := reselectSuppressedClusterPrimariesTx(ctx, tx, scan, findings)
		if err != nil {
			return err
		}
		if len(promoted) > 0 {
			changed = true
			if scan.BaselineResolvedAt != nil {
				if _, err := deriveFindingBaseline(ctx, tx, scan, scans, *scan.BaselineResolvedAt); err != nil {
					return err
				}
			} else {
				for _, primary := range promoted {
					if _, err := tx.ExecContext(ctx, tx.Rebind(`UPDATE findings SET baseline_state='',introduced_in_scan_id='',resolved_in_scan_id='',first_seen_at=NULL,last_seen_at=NULL,baseline_identity_key='' WHERE scan_id=? AND cluster_id=?`), scan.ID, primary); err != nil {
						return err
					}
				}
			}
		}
		if changed {
			if err := refreshSuppressedCanonicalTx(ctx, tx, scan); err != nil {
				return err
			}
			if err := refreshScanFindingCount(ctx, tx, scan.ID); err != nil {
				return err
			}
			if err := advanceFindingsRevision(ctx, tx, scan.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Ingestion reads rules while holding its scan lock. A concurrent rule mutation
// waits on that lock and reconciles the inserted rows, so stale executor copies
// cannot undo a create/revoke/expiry. Used by native and imported observations.
func applyFindingSuppressionsTx(ctx context.Context, tx *sqlx.Tx, findings []models.Finding) error {
	rulesByRepo := map[string][]models.FindingSuppression{}
	scans := map[string]models.Scan{}
	byScan := map[string][]int{}
	now := time.Now().UTC()
	for i, f := range findings {
		byScan[f.ScanID] = append(byScan[f.ScanID], i)
		scan, ok := scans[f.ScanID]
		if !ok {
			if err := tx.GetContext(ctx, &scan, tx.Rebind("SELECT * FROM scans WHERE id = ?"), f.ScanID); err != nil {
				return err
			}
			scans[f.ScanID] = scan
		}
		if f.RepoID != scan.RepoID {
			return fmt.Errorf("finding repository does not match scan")
		}
		rules, ok := rulesByRepo[scan.RepoID]
		if !ok {
			var err error
			rules, err = loadFindingSuppressions(ctx, tx, scan.RepoID)
			if err != nil {
				return err
			}
			rulesByRepo[scan.RepoID] = rules
		}
		suppression.Apply(findings[i:i+1], rules, map[string]string{scan.ID: scan.Branch}, now)
	}
	for scanID, idxs := range byScan {
		scan := scans[scanID]
		if scan.IgnorePolicyJSON == "" {
			continue
		}
		subset := make([]models.Finding, len(idxs))
		for j, i := range idxs {
			subset[j] = findings[i]
		}
		if _, _, err := scansuppress.ApplyScan(ctx, subset, &scan, ""); err != nil {
			return err
		}
		for j, i := range idxs {
			findings[i] = subset[j]
		}
	}
	return nil
}

// Retain persisted membership and choose the same visible representative as the
// native deduper. Existing primaries win ties, avoiding gratuitous ID changes.
func reselectSuppressedClusterPrimariesTx(ctx context.Context, tx *sqlx.Tx, scan models.Scan, findings []models.Finding) ([]string, error) {
	groups := map[string][]models.Finding{}
	var roots []string
	for _, f := range findings {
		if f.ClusterID == "" {
			continue
		}
		if _, ok := groups[f.ClusterID]; !ok {
			roots = append(roots, f.ClusterID)
		}
		groups[f.ClusterID] = append(groups[f.ClusterID], f)
	}
	var path string
	if err := tx.GetContext(ctx, &path, tx.Rebind("SELECT source_path FROM repos WHERE id=?"), scan.RepoID); err != nil {
		return nil, err
	}
	projectedAll := append([]models.Finding(nil), findings...)
	if _, _, err := scansuppress.ApplyScan(ctx, projectedAll, &scan, path); err != nil {
		return nil, err
	}
	byID := make(map[string]models.Finding, len(projectedAll))
	for _, f := range projectedAll {
		byID[f.ID] = f
	}
	var promoted []string
	for _, root := range roots {
		members := groups[root]
		var current models.Finding
		for _, f := range members {
			if f.ID == root {
				current = f
				break
			}
		}
		if current.ID == "" {
			return nil, fmt.Errorf("suppression cluster has no primary")
		}
		projected := make([]models.Finding, len(members))
		for i, f := range members {
			projected[i] = byID[f.ID]
		}
		chosen := current
		for _, f := range projected {
			if f.ID == root {
				chosen = f
				break
			}
		}
		for _, f := range projected {
			if identity.PreferPrimary(f, chosen) {
				chosen = f
			}
		}
		if chosen.ID == root {
			continue
		}
		// Projection flags must not overwrite an observation's durable/import decision.
		for _, f := range members {
			if f.ID == chosen.ID {
				chosen = f
				break
			}
		}
		chosen.Confidence = current.Confidence
		if err := writeFindingCluster(ctx, tx, chosen, members); err != nil {
			return nil, err
		}
		promoted = append(promoted, chosen.ID)
	}
	return promoted, nil
}

// Refresh metadata of existing canonical observations without changing their
// identity, current scan, or evidence membership. Running scans publish no new row.
func refreshSuppressedCanonicalTx(ctx context.Context, tx *sqlx.Tx, scan models.Scan) error {
	var published []models.Vulnerability
	if err := tx.SelectContext(ctx, &published, tx.Rebind("SELECT * FROM vulnerabilities WHERE repo_id=? AND scan_id=? ORDER BY id"), scan.RepoID, scan.ID); err != nil {
		return err
	}
	for _, v := range published {
		var primary models.Finding
		err := tx.GetContext(ctx, &primary, tx.Rebind(`SELECT f.* FROM findings f JOIN vulnerability_evidence e ON e.finding_id=f.id
   WHERE e.vulnerability_id=? AND f.scan_id=? AND (f.cluster_id='' OR f.cluster_id=f.id) ORDER BY f.id LIMIT 1`), v.ID, scan.ID)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		hydrateFindingAfterRead(&primary)
		updated := vulnerabilityFromFinding(&scan, primary, v.CanonicalKey)
		updated.ID = v.ID
		if err := upsertVulnerability(ctx, tx, updated); err != nil {
			return err
		}
	}
	return nil
}
