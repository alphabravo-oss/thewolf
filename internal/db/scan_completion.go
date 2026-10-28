package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/suppress"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type ScanCompletion struct {
	Gate         *models.QualityGateResult
	GateArtifact *models.ScanArtifact
	AutoBaseline bool
	SARIFImport  *models.SARIFImport
}

func (s *PostgresStore) CompleteScan(ctx context.Context, scan *models.Scan, completion ScanCompletion) (bool, error) {
	return completeScan(ctx, s.db, scan, completion)
}
func completeScan(ctx context.Context, database *sqlx.DB, scan *models.Scan, completion ScanCompletion) (bool, error) {
	gate, artifact := completion.Gate, completion.GateArtifact
	if scan == nil || gate == nil || artifact == nil || scan.Status != models.ScanStatusCompleted || scan.CompletedAt == nil || scan.FailureCode != "" || scan.CancelRequestedAt != nil ||
		gate.ScanStatus != models.ScanStatusCompleted || gate.ScanID != scan.ID || gate.FindingsRevision != scan.FindingsRevision || gate.Status == "pending" || gate.Status == "" ||
		artifact.ScanID != scan.ID || artifact.FindingsRevision == nil || *artifact.FindingsRevision != scan.FindingsRevision || artifact.ChecksumSHA256 == "" {
		return false, errors.New("scan completion requires a terminal observation and matching gate artifact")
	}
	if imp := completion.SARIFImport; imp != nil && (imp.ScanID != scan.ID || imp.RepoID != scan.RepoID || imp.CreatedBy != scan.UserID) {
		return false, errors.New("import completion crosses scan ownership")
	}
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockVulnerabilityRepo(ctx, tx, scan.RepoID); err != nil {
		return false, err
	}
	current, err := lockClusterScan(ctx, tx, scan.RepoID, scan.ID)
	if err != nil {
		return false, err
	}
	if current.Status != models.ScanStatusRunning || current.LeaseToken != scan.LeaseToken || current.CancelRequestedAt != nil {
		return false, nil
	}
	if current.FindingsRevision != scan.FindingsRevision || current.IgnorePolicyJSON != scan.IgnorePolicyJSON {
		return false, ErrStaleFindings
	}
	if current.UserID != scan.UserID || current.Branch != scan.Branch || suppress.ScanSourceKey(current) != suppress.ScanSourceKey(scan) {
		return false, ErrStaleFindings
	}
	if ok, err := finalizeScan(ctx, tx, scan, scan.LeaseToken); err != nil || !ok {
		return ok, err
	}
	if err := reconcileVulnerabilitiesTx(ctx, tx, scan.ID); err != nil {
		return false, err
	}
	if err := upsertQualityGateResultTx(ctx, tx, gate); err != nil {
		return false, err
	}
	if err := createScanArtifact(ctx, tx, artifact); err != nil {
		return false, err
	}
	profile := strings.ToLower(strings.TrimSpace(scan.Profile))
	if completion.AutoBaseline && profile != "fast" && profile != "pr" && profile != "targeted" {
		var saved struct {
			ID     string `db:"id"`
			ScanID string `db:"scan_id"`
		}
		err := tx.GetContext(ctx, &saved, tx.Rebind("SELECT id, scan_id FROM scan_baselines WHERE repo_id = ? AND branch = ? AND name = 'auto'"), scan.RepoID, scan.Branch)
		if err == nil {
			// Replace an automatic baseline that no longer qualifies (saved by older
			// rules) once this scan does; otherwise keep the first success.
			var stale, qualifies bool
			if stale, err = autoBaselineStale(ctx, tx, scan.RepoID, saved.ScanID); err == nil && stale {
				if qualifies, err = autoBaselineCandidate(ctx, tx, scan.RepoID, scan.ID); err == nil && qualifies {
					_, err = tx.ExecContext(ctx, tx.Rebind("UPDATE scan_baselines SET scan_id = ?, strategy = 'auto-first-success', updated_at = ? WHERE id = ?"), scan.ID, time.Now().UTC(), saved.ID)
				}
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			err = createScanBaselineTx(ctx, tx, &models.ScanBaseline{ID: uuid.NewString(), RepoID: scan.RepoID, Branch: scan.Branch, Name: "auto", ScanID: scan.ID, Strategy: "auto-first-success", CreatedBy: scan.UserID})
			if errors.Is(err, ErrInvalidBaseline) {
				err = nil // not a clean first success (failed tool, foreign owner); a later scan qualifies, results still publish
			}
		}
		if err != nil {
			return false, err
		}
	}
	if completion.SARIFImport != nil {
		if err := createSARIFImport(ctx, tx, completion.SARIFImport); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	scan.ClaimedBy, scan.LeaseToken = "", ""
	scan.LeaseExpiresAt, scan.HeartbeatAt = nil, nil
	return true, nil
}

func finalizeScan(ctx context.Context, database sqlx.ExtContext, scan *models.Scan, leaseToken string) (bool, error) {
	scan.UpdatedAt = time.Now().UTC()
	result, err := database.ExecContext(ctx, database.Rebind(
		`UPDATE scans SET status = ?, phase = ?, tools_selected = ?, tools_completed = ?,
		     tools_failed = ?, tools_errors = ?, finding_count = ?, coverage_summary = ?,
		     source_type = ?, remote_node_id = ?, source_path = ?, commit_sha = ?,
		     tree_digest = ?, dirty_state = ?, prepared_workspace = ?, failure_code = ?,
		     failure_message = ?, completed_at = ?, claimed_by = '', lease_token = '',
		     lease_expires_at = NULL, heartbeat_at = NULL, updated_at = ?
		 WHERE id = ? AND lease_token = ? AND status = ? AND cancel_requested_at IS NULL`),
		scan.Status, scan.Phase, scan.ToolsSelected, scan.ToolsCompleted,
		scan.ToolsFailed, scan.ToolsErrors, scan.FindingCount, scan.CoverageSummary,
		scan.SourceType, scan.RemoteNodeID, scan.SourcePath, scan.CommitSHA,
		scan.TreeDigest, scan.DirtyState, scan.PreparedWorkspace, scan.FailureCode, scan.FailureMessage,
		scan.CompletedAt, scan.UpdatedAt,
		scan.ID, leaseToken, models.ScanStatusRunning)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func createScanArtifact(ctx context.Context, database sqlx.ExtContext, artifact *models.ScanArtifact) error {
	now := time.Now().UTC()
	artifact.CreatedAt = now
	artifact.UpdatedAt = now
	if artifact.RedactionLevel == "" {
		artifact.RedactionLevel = "internal_report"
	}
	_, err := sqlx.NamedExecContext(ctx, database,
		`INSERT INTO scan_artifacts (id, scan_id, artifact_type, file_path, storage_key, file_size, checksum_sha256, redaction_level, created_at, updated_at, findings_revision)
		 VALUES (:id, :scan_id, :artifact_type, :file_path, :storage_key, :file_size, :checksum_sha256, :redaction_level, :created_at, :updated_at, :findings_revision)`, artifact)
	return err
}

func createSARIFImport(ctx context.Context, database sqlx.ExtContext, imp *models.SARIFImport) error {
	now := time.Now().UTC()
	imp.CreatedAt = now
	imp.UpdatedAt = now
	_, err := sqlx.NamedExecContext(ctx, database,
		`INSERT INTO sarif_imports (id, repo_id, scan_id, source, checksum_sha256, result_count, imported_count, created_by, created_at, updated_at)
		 VALUES (:id, :repo_id, :scan_id, :source, :checksum_sha256, :result_count, :imported_count, :created_by, :created_at, :updated_at)`, imp)
	return err
}

// autoBaselineStale reports whether a saved automatic baseline points at a scan
// that would not qualify today (missing, or not a complete success).
func autoBaselineStale(ctx context.Context, tx *sqlx.Tx, repoID, scanID string) (bool, error) {
	saved, err := lockClusterScan(ctx, tx, repoID, scanID)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return !validBaselineScan(*saved), nil
}

// autoBaselineCandidate applies createScanBaselineTx's rules without inserting.
func autoBaselineCandidate(ctx context.Context, tx *sqlx.Tx, repoID, scanID string) (bool, error) {
	current, err := lockClusterScan(ctx, tx, repoID, scanID)
	if err != nil {
		return false, err
	}
	var owner string
	if err := tx.GetContext(ctx, &owner, tx.Rebind("SELECT user_id FROM repos WHERE id = ?"), repoID); err != nil {
		return false, err
	}
	return validBaselineScan(*current) && current.UserID == owner, nil
}
