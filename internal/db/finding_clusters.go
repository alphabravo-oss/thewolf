package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/alphabravocompany/thewolf/internal/finding/identity"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/jmoiron/sqlx"
)

func (s *PostgresStore) FinalizeFindingClusters(ctx context.Context, scanID, leaseToken string, clusters []models.Finding) error {
	return finalizeFindingClusters(ctx, s.db, scanID, leaseToken, clusters)
}

func (s *PostgresStore) ListFindingEvidenceByScan(ctx context.Context, scanID string) ([]models.Finding, error) {
	return listFindingEvidenceByScan(ctx, s.db, scanID)
}
func listFindingEvidenceByScan(ctx context.Context, database *sqlx.DB, scanID string) ([]models.Finding, error) {
	var findings []models.Finding
	err := database.SelectContext(ctx, &findings, database.Rebind("SELECT * FROM findings WHERE scan_id = ? ORDER BY tool_name, id"), scanID)
	hydrateFindingsAfterRead(findings)
	return findings, err
}

func (s *PostgresStore) ListFindingClusterEvidence(ctx context.Context, findingID string) ([]models.Finding, error) {
	return listFindingClusterEvidence(ctx, s.db, findingID)
}
func listFindingClusterEvidence(ctx context.Context, database *sqlx.DB, findingID string) ([]models.Finding, error) {
	var findings []models.Finding
	err := database.SelectContext(ctx, &findings, database.Rebind(`SELECT f.* FROM findings f
 JOIN findings requested ON requested.id = ? AND f.scan_id = requested.scan_id AND f.repo_id = requested.repo_id
 WHERE f.id = COALESCE(NULLIF(requested.cluster_id, ''), requested.id)
 OR (requested.cluster_id <> '' AND f.cluster_id = requested.cluster_id)
 ORDER BY f.tool_name, f.id`), findingID)
	hydrateFindingsAfterRead(findings)
	return findings, err
}

// Finalization changes membership, not evidence identity. Original finding IDs
// remain valid for fix items, audit links and explicit evidence reads.
func finalizeFindingClusters(ctx context.Context, database *sqlx.DB, scanID, leaseToken string, clusters []models.Finding) error {
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// The same scan-row lock serializes tool persistence and lease replacement.
	locked, err := tx.ExecContext(ctx, database.Rebind(`UPDATE scans SET updated_at = updated_at
 WHERE id = ? AND status = ? AND lease_token = ? AND cancel_requested_at IS NULL`), scanID, models.ScanStatusRunning, leaseToken)
	if err != nil {
		return err
	}
	n, err := locked.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStaleScanLease
	}
	var scan models.Scan
	if err := tx.GetContext(ctx, &scan, database.Rebind("SELECT * FROM scans WHERE id = ?"), scanID); err != nil {
		return err
	}
	repoID := scan.RepoID
	query := "SELECT * FROM findings WHERE scan_id = ?"
	if database.DriverName() == "postgres" {
		query += " FOR UPDATE"
	}
	var evidence []models.Finding
	if err := tx.SelectContext(ctx, &evidence, database.Rebind(query), scanID); err != nil {
		return err
	}
	byID := make(map[string]models.Finding, len(evidence))
	for _, f := range evidence {
		if f.RepoID != repoID {
			return fmt.Errorf("finding evidence crosses repository scope in scan %s", scanID)
		}
		byID[f.ID] = f
	}
	primary := make(map[string]string, len(clusters))
	for _, f := range clusters {
		stored, ok := byID[f.ID]
		key := identity.ClusterKey(f)
		if !ok || f.ScanID != scanID || f.RepoID != stored.RepoID || identity.ClusterKey(stored) != key {
			return fmt.Errorf("cluster primary is not matching evidence in scan %s", scanID)
		}
		if primary[key] != "" {
			return fmt.Errorf("duplicate cluster for scan %s", scanID)
		}
		primary[key] = f.ID
	}
	for _, f := range evidence {
		id := primary[identity.ClusterKey(f)]
		if id == "" {
			return fmt.Errorf("unassigned evidence in scan %s", scanID)
		}
		storedPrimary := byID[id]
		visible := !f.Suppressed && (f.Status == models.StatusOpen || f.Status == "")
		primaryVisible := !storedPrimary.Suppressed && (storedPrimary.Status == models.StatusOpen || storedPrimary.Status == "")
		if visible && !primaryVisible && scan.IgnorePolicyJSON == "" {
			return fmt.Errorf("cluster primary no longer represents open evidence in scan %s", scanID)
		}
		if _, err := tx.ExecContext(ctx, database.Rebind("UPDATE findings SET cluster_id = ? WHERE id = ?"), id, f.ID); err != nil {
			return err
		}
	}
	for _, f := range clusters {
		// Triage can change while enrichment runs; preserve its current values.
		stored := byID[f.ID]
		f.Suppressed, f.SuppressionID, f.SuppressedReason = stored.Suppressed, stored.SuppressionID, stored.SuppressedReason
		f.CorroboratedByJSON = ""
		if err := updateFinding(ctx, tx, &f); err != nil {
			return err
		}
	}
	if scan.IgnorePolicyJSON != "" {
		if err := tx.SelectContext(ctx, &evidence, tx.Rebind("SELECT * FROM findings WHERE scan_id=? ORDER BY id"), scanID); err != nil {
			return err
		}
		hydrateFindingsAfterRead(evidence)
		if _, err := reselectSuppressedClusterPrimariesTx(ctx, tx, scan, evidence); err != nil {
			return err
		}
	}
	if err := advanceFindingsRevision(ctx, tx, scanID); err != nil {
		return err
	}
	return tx.Commit()
}

// Triage through an old member URL applies to the same issue as its primary.
// Serialize against finalization before resolving membership.
func updateFindingClusterStatus(ctx context.Context, database *sqlx.DB, id string, status models.Status) error {
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, database.Rebind(`UPDATE scans SET updated_at = updated_at
 WHERE id = (SELECT scan_id FROM findings WHERE id = ?)`), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	_, err = tx.ExecContext(ctx, database.Rebind(`WITH target AS (
 SELECT COALESCE(NULLIF(cluster_id, ''), id) AS primary_id, scan_id, repo_id FROM findings WHERE id = ?)
 UPDATE findings SET status = ?, updated_at = ?
 WHERE EXISTS (SELECT 1 FROM target WHERE findings.scan_id = target.scan_id AND findings.repo_id = target.repo_id
 AND (findings.id = target.primary_id OR findings.cluster_id = target.primary_id))`), id, status, time.Now().UTC())
	if err != nil {
		return err
	}
	var scanID string
	if err := tx.GetContext(ctx, &scanID, tx.Rebind("SELECT scan_id FROM findings WHERE id = ?"), id); err != nil {
		return err
	}
	if err := advanceFindingsRevision(ctx, tx, scanID); err != nil {
		return err
	}
	return tx.Commit()
}
