package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"runtime/trace"
	"slices"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/suppress"
	"github.com/jmoiron/sqlx"
)

// Uncaptured live scans still need the shared filesystem matcher. Never page
// first and hide suppressed rows afterward: that would corrupt page boundaries.
var ErrLiveFindingPolicy = errors.New("scan has not captured its ignore policy")

func (s *SQLiteStore) QueryScanFindings(ctx context.Context, scanID string, q FindingListQuery) (FindingPage, error) {
	return queryScanFindings(ctx, s.readDatabase(), scanID, q)
}
func (s *PostgresStore) QueryScanFindings(ctx context.Context, scanID string, q FindingListQuery) (FindingPage, error) {
	return queryScanFindings(ctx, s.db, scanID, q)
}

func queryScanFindings(ctx context.Context, database *sqlx.DB, scanID string, q FindingListQuery) (FindingPage, error) {
	result := FindingPage{Findings: []models.Finding{}}
	tx, err := database.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	var scan models.Scan
	policyRead := trace.StartRegion(ctx, "finding.database")
	err = tx.GetContext(ctx, &scan, tx.Rebind("SELECT * FROM scans WHERE id=?"), scanID)
	policyRead.End()
	if err != nil {
		return result, err
	}
	if err := validateFindingPolicies(ctx, []models.Scan{scan}); err != nil {
		return result, err
	}
	result, err = queryFindingPage(ctx, tx, "scan_id = ? AND (cluster_id = '' OR cluster_id = id)", []any{scanID}, q)
	if err != nil {
		return result, err
	}
	normalizeFindingPage(ctx, result.Findings)
	return result, tx.Commit()
}

func queryFindingPage(ctx context.Context, tx *sqlx.Tx, where string, args []any, q FindingListQuery) (FindingPage, error) {
	region := trace.StartRegion(ctx, "finding.database")
	defer region.End()
	result := FindingPage{Findings: []models.Finding{}}
	var err error
	if !q.IncludeSuppressed {
		if !q.StatsOnly {
			if err := tx.GetContext(ctx, &result.Suppressed, tx.Rebind("SELECT COUNT(*) FROM findings WHERE "+where+" AND suppressed = TRUE"), args...); err != nil {
				return result, err
			}
		}
		where += " AND suppressed = FALSE"
	}
	if q.StatsOnly {
		result.Stats, err = queryFindingStats(ctx, tx, where, args)
		if err != nil {
			return result, err
		}
		return result, nil
	}
	for _, filter := range []struct {
		column string
		values []string
	}{
		{"severity", q.Severities}, {"category", q.Categories}, {"status", q.Statuses}, {"baseline_state", q.BaselineStates},
	} {
		if len(filter.values) == 0 {
			continue
		}
		clause, values, err := sqlx.In(filter.column+" IN (?)", filter.values)
		if err != nil {
			return result, err
		}
		where += " AND " + clause
		args = append(args, values...)
	}
	if len(q.Tools) > 0 {
		contributors := "SELECT value FROM json_each(CASE WHEN corroborated_by_json = '' THEN '[]' ELSE corroborated_by_json END)"
		if tx.DriverName() == "postgres" {
			contributors = "SELECT value FROM jsonb_array_elements_text(CASE WHEN corroborated_by_json = '' THEN '[]' ELSE corroborated_by_json END::jsonb) AS contributor(value)"
		}
		clause, values, err := sqlx.In("(tool_name IN (?) OR EXISTS (SELECT 1 FROM ("+contributors+") AS tools WHERE tools.value IN (?)))", q.Tools, q.Tools)
		if err != nil {
			return result, err
		}
		where += " AND " + clause
		args = append(args, values...)
	}
	if term := strings.TrimSpace(q.Search); term != "" {
		term = "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(term)) + "%"
		where += " AND LOWER(title || ' ' || COALESCE(rule_id, '') || ' ' || file_path || ' ' || tool_name || ' ' || description) LIKE ? ESCAPE '!'"
		args = append(args, term)
	}
	if err := tx.GetContext(ctx, &result.Total, tx.Rebind("SELECT COUNT(*) FROM findings WHERE "+where), args...); err != nil {
		return result, err
	}
	column := map[string]string{
		"severity":   "CASE severity WHEN 'critical' THEN 5 WHEN 'high' THEN 4 WHEN 'medium' THEN 3 WHEN 'low' THEN 2 WHEN 'info' THEN 1 ELSE 0 END",
		"created_at": "created_at", "tool_name": "tool_name", "title": "title", "file_path": "file_path", "line_start": "line_start",
		"first_seen": "COALESCE(first_seen_at, created_at)",
	}[q.SortBy]
	if column == "" {
		column = "composite_score"
	}
	direction := " ASC"
	if q.Desc {
		direction = " DESC"
	}
	columns := findingPageColumns
	if q.IncludeSARIF {
		columns += ", sarif_data"
	}
	query := "SELECT " + columns + " FROM findings WHERE " + where + " ORDER BY " + column + direction + ", id ASC LIMIT ? OFFSET ?"
	args = append(args, pageLimit(q.Limit), max(0, q.Offset))
	if err := tx.SelectContext(ctx, &result.Findings, tx.Rebind(query), args...); err != nil {
		return result, err
	}
	return result, nil
}

// Explicit list projection keeps large native SARIF out of ordinary pages.
const findingPageColumns = `cluster_id, id, scan_id, repo_id, fingerprint, stable_fingerprint, location_fingerprint, semantic_fingerprint,
 evidence_fingerprint, identity_version, tool_name, category, severity, title, description, file_path,
 line_start, line_end, code_snippet, cwe_id, rule_id, package_name, package_version, package_ecosystem,
 package_url, advisory_id, tool_severity_score, location_weight, ai_context_score, composite_score, ai_fix_suggestion, module_name,
 function_name, symbol_kind, file_purpose, dependents_json, status, created_at, updated_at, fine_category,
 fix_strategy_id, confidence, corroborated_by_json, suppressed, suppression_id, suppressed_reason, baseline_identity_key, baseline_state,
 first_seen_at, last_seen_at, introduced_in_scan_id, resolved_in_scan_id, source_kind, source_ref`

func (s *SQLiteStore) QueryCurrentFindings(ctx context.Context, userID string, fleet bool, collectionID, repoID string, q FindingListQuery) (FindingPage, error) {
	return queryCurrentFindings(ctx, s.readDatabase(), userID, fleet, collectionID, repoID, q)
}
func (s *PostgresStore) QueryCurrentFindings(ctx context.Context, userID string, fleet bool, collectionID, repoID string, q FindingListQuery) (FindingPage, error) {
	return queryCurrentFindings(ctx, s.db, userID, fleet, collectionID, repoID, q)
}

func queryCurrentFindings(ctx context.Context, database *sqlx.DB, userID string, fleet bool, collectionID, repoID string, q FindingListQuery) (FindingPage, error) {
	result := FindingPage{Findings: []models.Finding{}}
	tx, err := database.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	// Reuse history semantics: incomplete scans add evidence without erasing the
	// last complete observation. Only compact identities are loaded for history;
	// descriptions, snippets and native reports are fetched for the page only.
	e, err := loadFleetEvidence(ctx, tx, userID, fleet, collectionID, repoID)
	if err != nil {
		return result, err
	}
	normalization := trace.StartRegion(ctx, "finding.normalization")
	current := e.currentEvidence(time.Now().UTC())
	current = slices.DeleteFunc(current, func(f *fleetFinding) bool {
		return f.Status != models.StatusOpen || (repoID != "" && f.RepoID != repoID)
	})
	current = dedupCurrentFindings(current)
	ids := make([]string, len(current))
	for i, f := range current {
		ids[i] = f.ID
	}
	encoded, err := json.Marshal(ids)
	normalization.End()
	if err != nil {
		return result, err
	}
	// One bound JSON array avoids backend bind-variable limits for large fleets.
	idQuery := "SELECT value FROM json_each(?)"
	if tx.DriverName() == "postgres" {
		idQuery = "SELECT value FROM jsonb_array_elements_text(?::jsonb) AS ids(value)"
	}
	where := "id IN (" + idQuery + ")"
	args := []any{string(encoded)}
	var scans []models.Scan
	policyRead := trace.StartRegion(ctx, "finding.database")
	err = tx.SelectContext(ctx, &scans, tx.Rebind("SELECT * FROM scans WHERE id IN (SELECT DISTINCT scan_id FROM findings WHERE "+where+")"), args...)
	policyRead.End()
	if err != nil {
		return result, err
	}
	if err := validateFindingPolicies(ctx, scans); err != nil {
		return result, err
	}
	result, err = queryFindingPage(ctx, tx, where, args, q)
	if err != nil {
		return result, err
	}
	normalizeFindingPage(ctx, result.Findings)
	return result, tx.Commit()
}

func validateFindingPolicies(ctx context.Context, scans []models.Scan) error {
	region := trace.StartRegion(ctx, "finding.suppression")
	defer region.End()
	for i := range scans {
		scan := &scans[i]
		if scan.IgnorePolicyJSON == "" && scan.Status != models.ScanStatusCompleted && scan.Status != models.ScanStatusFailed && scan.Status != models.ScanStatusCancelled {
			return ErrLiveFindingPolicy
		}
		// Captured flags are maintained by ingestion and durable suppression edits.
		// Validate the source binding even when the resulting page is empty.
		if _, _, err := suppress.ApplyScan(ctx, nil, scan, ""); err != nil {
			return err
		}
	}
	return nil
}

func normalizeFindingPage(ctx context.Context, findings []models.Finding) {
	region := trace.StartRegion(ctx, "finding.normalization")
	defer region.End()
	hydrateFindingsAfterRead(findings)
}
