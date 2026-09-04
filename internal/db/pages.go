package db

import (
	"context"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *SQLiteStore) ListScansPage(ctx context.Context, q ScanListQuery) ([]models.Scan, int, error) {
	return listScansPage(ctx, s.db, q)
}
func (s *PostgresStore) ListScansPage(ctx context.Context, q ScanListQuery) ([]models.Scan, int, error) {
	return listScansPage(ctx, s.db, q)
}

func listScansPage(ctx context.Context, database *sqlx.DB, q ScanListQuery) ([]models.Scan, int, error) {
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
	if q.RootsOnly {
		conds = append(conds, "(origin_scan_id IS NULL OR origin_scan_id = '')")
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	var total int
	if err := database.GetContext(ctx, &total, database.Rebind("SELECT COUNT(*) FROM scans"+where), args...); err != nil {
		return nil, 0, err
	}
	listQ := database.Rebind("SELECT * FROM scans" + where + " ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?")
	off := q.Offset
	if off < 0 {
		off = 0
	}
	pageArgs := append(append([]any{}, args...), pageLimit(q.Limit), off)
	var scans []models.Scan
	if err := database.SelectContext(ctx, &scans, listQ, pageArgs...); err != nil {
		return nil, 0, err
	}
	if err := attachScanRepos(ctx, database, scans); err != nil {
		return nil, 0, err
	}
	return scans, total, nil
}

func attachScanRepos(ctx context.Context, database *sqlx.DB, scans []models.Scan) error {
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
	if err := database.SelectContext(ctx, &repos, database.Rebind(query), args...); err != nil {
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
	return listVulnerabilitiesPage(ctx, s.db, q)
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
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	var total int
	if err := database.GetContext(ctx, &total, database.Rebind("SELECT COUNT(*) FROM vulnerabilities v"+where), args...); err != nil {
		return nil, 0, err
	}
	listQ := database.Rebind("SELECT v.* FROM vulnerabilities v" + where + " ORDER BY v.composite_score DESC LIMIT ? OFFSET ?")
	off := q.Offset
	if off < 0 {
		off = 0
	}
	pageArgs := append(append([]any{}, args...), pageLimit(q.Limit), off)
	var vulns []models.Vulnerability
	if err := database.SelectContext(ctx, &vulns, listQ, pageArgs...); err != nil {
		return nil, 0, err
	}
	for i := range vulns {
		hydrateVulnerability(&vulns[i])
	}
	return vulns, total, nil
}

func (s *SQLiteStore) ScanFindingStats(ctx context.Context, scanID string) (ScanFindingStats, error) {
	return scanFindingStats(ctx, s.db, scanID)
}
func (s *PostgresStore) ScanFindingStats(ctx context.Context, scanID string) (ScanFindingStats, error) {
	return scanFindingStats(ctx, s.db, scanID)
}

func scanFindingStats(ctx context.Context, database *sqlx.DB, scanID string) (ScanFindingStats, error) {
	stats := ScanFindingStats{
		BySeverity: map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0, "info": 0},
		ByTool:     map[string]int{},
		ByCategory: map[string]int{},
	}
	q := database.Rebind(`SELECT severity, tool_name, category, COUNT(*) AS n
		FROM findings WHERE scan_id = ? GROUP BY severity, tool_name, category`)
	var rows []struct {
		Severity string `db:"severity"`
		ToolName string `db:"tool_name"`
		Category string `db:"category"`
		N        int    `db:"n"`
	}
	if err := database.SelectContext(ctx, &rows, q, scanID); err != nil {
		return stats, err
	}
	for _, r := range rows {
		stats.Total += r.N
		stats.BySeverity[r.Severity] += r.N
		stats.ByTool[r.ToolName] += r.N
		stats.ByCategory[r.Category] += r.N
	}
	return stats, nil
}
