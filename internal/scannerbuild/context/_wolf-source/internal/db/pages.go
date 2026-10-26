package db

import (
	"context"
	"database/sql"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *SQLiteStore) ListScansPage(ctx context.Context, q ScanListQuery) ([]models.Scan, int, error) {
	return listScansPage(ctx, s.readDatabase(), q)
}
func (s *PostgresStore) ListScansPage(ctx context.Context, q ScanListQuery) ([]models.Scan, int, error) {
	return listScansPage(ctx, s.db, q)
}

func listScansPage(ctx context.Context, database *sqlx.DB, q ScanListQuery) ([]models.Scan, int, error) {
	tx, err := database.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var conds []string
	var args []any
	if !q.Fleet {
		conds = append(conds, "user_id = ?")
		args = append(args, q.UserID)
	}
	if q.RepoID != "" {
		conds = append(conds, "repo_id = ?")
		args = append(args, q.RepoID)
	}
	if q.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, q.Status)
	}
	if q.Search != "" {
		term := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(q.Search)) + "%"
		conds = append(conds, "(LOWER(id) LIKE ? ESCAPE '!' OR LOWER(branch) LIKE ? ESCAPE '!' OR repo_id IN (SELECT id FROM repos WHERE LOWER(name) LIKE ? ESCAPE '!'))")
		args = append(args, term, term, term)
	}
	if q.RootsOnly {
		conds = append(conds, "(origin_scan_id IS NULL OR origin_scan_id = '')")
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	var total int
	if err := tx.GetContext(ctx, &total, tx.Rebind("SELECT COUNT(*) FROM scans"+where), args...); err != nil {
		return nil, 0, err
	}
	order := "created_at DESC, id DESC"
	if column := map[string]string{"status": "status", "id": "id", "repo": "(SELECT name FROM repos WHERE repos.id = scans.repo_id)", "findings": "finding_count", "started": "started_at"}[q.SortBy]; column != "" {
		direction := " ASC"
		if q.Desc {
			direction = " DESC"
		}
		order = column + direction + ", id DESC"
	}
	listQ := tx.Rebind("SELECT * FROM scans" + where + " ORDER BY " + order + " LIMIT ? OFFSET ?")
	off := q.Offset
	if off < 0 {
		off = 0
	}
	pageArgs := append(append([]any{}, args...), pageLimit(q.Limit), off)
	scans := []models.Scan{}
	if err := tx.SelectContext(ctx, &scans, listQ, pageArgs...); err != nil {
		return nil, 0, err
	}
	if err := attachScanRepos(ctx, tx, scans); err != nil {
		return nil, 0, err
	}
	return scans, total, tx.Commit()
}

func attachScanRepos(ctx context.Context, database sqlx.ExtContext, scans []models.Scan) error {
	if len(scans) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(scans))
	for _, s := range scans {
		if s.RepoID == "" {
			continue
		}
		if _, ok := seen[s.RepoID]; ok {
			continue
		}
		seen[s.RepoID] = struct{}{}
		ids = append(ids, s.RepoID)
	}
	if len(ids) == 0 {
		return nil
	}
	query, args, err := sqlx.In("SELECT * FROM repos WHERE id IN (?)", ids)
	if err != nil {
		return err
	}
	var repos []models.Repo
	if err := sqlx.SelectContext(ctx, database, &repos, database.Rebind(query), args...); err != nil {
		return err
	}
	byID := make(map[string]models.Repo, len(repos))
	for _, r := range repos {
		byID[r.ID] = r
	}
	for i := range scans {
		if r, ok := byID[scans[i].RepoID]; ok {
			cp := r
			scans[i].Repo = &cp
		}
	}
	return nil
}

func (s *SQLiteStore) ListVulnerabilitiesPage(ctx context.Context, q VulnListQuery) ([]models.Vulnerability, int, error) {
	return listVulnerabilitiesPage(ctx, s.readDatabase(), q)
}
func (s *PostgresStore) ListVulnerabilitiesPage(ctx context.Context, q VulnListQuery) ([]models.Vulnerability, int, error) {
	return listVulnerabilitiesPage(ctx, s.db, q)
}

func listVulnerabilitiesPage(ctx context.Context, database *sqlx.DB, q VulnListQuery) ([]models.Vulnerability, int, error) {
	var conds []string
	var args []any
	if !q.Fleet {
		conds = append(conds, "v.repo_id IN (SELECT id FROM repos WHERE user_id = ?)")
		args = append(args, q.UserID)
	}
	if q.RepoID != "" {
		conds = append(conds, "v.repo_id = ?")
		args = append(args, q.RepoID)
	}
	if q.ScanID != "" {
		conds = append(conds, "v.scan_id = ?")
		args = append(args, q.ScanID)
	}
	if search := strings.TrimSpace(q.Search); search != "" {
		term := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(search)) + "%"
		conds = append(conds, "LOWER(v.title || ' ' || v.severity || ' ' || v.category || ' ' || v.baseline_state || ' ' || v.corroborated_by_json) LIKE ? ESCAPE '!'")
		args = append(args, term)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	tx, err := database.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var total int
	if err := tx.GetContext(ctx, &total, database.Rebind("SELECT COUNT(*) FROM vulnerabilities v"+where), args...); err != nil {
		return nil, 0, err
	}
	order := "v.composite_score DESC, v.id ASC"
	if column := map[string]string{"severity": "CASE v.severity WHEN 'critical' THEN 5 WHEN 'high' THEN 4 WHEN 'medium' THEN 3 WHEN 'low' THEN 2 WHEN 'info' THEN 1 ELSE 0 END", "title": "v.title", "evidence": "v.evidence_count", "baseline": "v.baseline_state", "composite_score": "v.composite_score"}[q.SortBy]; column != "" {
		direction := " ASC"
		if q.Desc {
			direction = " DESC"
		}
		order = column + direction + ", v.id ASC"
	}
	listQ := tx.Rebind("SELECT v.* FROM vulnerabilities v" + where + " ORDER BY " + order + " LIMIT ? OFFSET ?")
	off := q.Offset
	if off < 0 {
		off = 0
	}
	pageArgs := append(append([]any{}, args...), pageLimit(q.Limit), off)
	vulns := []models.Vulnerability{}
	if err := tx.SelectContext(ctx, &vulns, listQ, pageArgs...); err != nil {
		return nil, 0, err
	}
	for i := range vulns {
		hydrateVulnerability(&vulns[i])
	}
	return vulns, total, tx.Commit()
}

func (s *SQLiteStore) ScanFindingStats(ctx context.Context, scanID string) (ScanFindingStats, error) {
	return scanFindingStats(ctx, s.db, scanID)
}
func (s *PostgresStore) ScanFindingStats(ctx context.Context, scanID string) (ScanFindingStats, error) {
	return scanFindingStats(ctx, s.db, scanID)
}

func scanFindingStats(ctx context.Context, database *sqlx.DB, scanID string) (ScanFindingStats, error) {
	return queryFindingStats(ctx, database, "scan_id = ? AND (cluster_id = '' OR cluster_id = id)", []any{scanID})
}

func queryFindingStats(ctx context.Context, database sqlx.ExtContext, where string, args []any) (ScanFindingStats, error) {
	stats := ScanFindingStats{
		BySeverity: map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0, "info": 0},
		ByTool:     map[string]int{},
		ByCategory: map[string]int{},
	}
	q := database.Rebind(`SELECT severity, tool_name, category, corroborated_by_json, COUNT(*) AS n
		FROM findings WHERE ` + where + ` GROUP BY severity, tool_name, category, corroborated_by_json`)
	var rows []struct {
		Severity           string `db:"severity"`
		ToolName           string `db:"tool_name"`
		CorroboratedByJSON string `db:"corroborated_by_json"`
		Category           string `db:"category"`
		N                  int    `db:"n"`
	}
	if err := sqlx.SelectContext(ctx, database, &rows, q, args...); err != nil {
		return stats, err
	}
	for _, r := range rows {
		stats.Total += r.N
		stats.BySeverity[r.Severity] += r.N
		f := models.Finding{ToolName: r.ToolName, CorroboratedByJSON: r.CorroboratedByJSON}
		hydrateFindingAfterRead(&f)
		for _, tool := range f.ReportingTools() {
			stats.ByTool[tool] += r.N
		}
		stats.ByCategory[r.Category] += r.N
	}
	return stats, nil
}
