package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// auditOrderClause maps the (validated) sort enum to a safe ORDER BY. SortBy /
// Desc are not raw user input — they come from a fixed vocabulary — so there is
// no injection surface here.
func auditOrderClause(q AuditQuery) string {
	col := "created_at"
	if q.SortBy == "status" {
		col = "status_code"
	}
	dir := "ASC"
	if q.Desc {
		dir = "DESC"
	}
	if col == "created_at" {
		return " ORDER BY created_at " + dir
	}
	// Tiebreak by time so paging is stable when sorting by a non-unique column.
	return " ORDER BY " + col + " " + dir + ", created_at DESC"
}

func auditLimit(q AuditQuery) int {
	if q.Limit <= 0 || q.Limit > 1000 {
		return 100
	}
	return q.Limit
}

func (s *SQLiteStore) QueryAuditLog(ctx context.Context, q AuditQuery) ([]models.AuditLogEntry, int, error) {
	var conds []string
	var args []any
	if term := strings.TrimSpace(q.Search); term != "" {
		like := "%" + strings.ToLower(term) + "%"
		conds = append(conds, "(LOWER(path) LIKE ? OR LOWER(action) LIKE ? OR LOWER(method) LIKE ? OR LOWER(event_type) LIKE ?)")
		args = append(args, like, like, like, like)
	}
	if q.Method != "" {
		conds = append(conds, "method = ?")
		args = append(args, strings.ToUpper(q.Method))
	}
	if q.Category != "" {
		conds = append(conds, "category = ?")
		args = append(args, q.Category)
	}
	if q.Severity != "" {
		conds = append(conds, "severity = ?")
		args = append(args, q.Severity)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := s.db.GetContext(ctx, &total, "SELECT COUNT(*) FROM audit_log"+where, args...); err != nil {
		return nil, 0, err
	}

	sql := "SELECT * FROM audit_log" + where + auditOrderClause(q) + " LIMIT ? OFFSET ?"
	pageArgs := append(append([]any{}, args...), auditLimit(q), q.Offset)
	var es []models.AuditLogEntry
	err := s.db.SelectContext(ctx, &es, sql, pageArgs...)
	return es, total, err
}

func (s *PostgresStore) QueryAuditLog(ctx context.Context, q AuditQuery) ([]models.AuditLogEntry, int, error) {
	var conds []string
	var args []any
	ph := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if term := strings.TrimSpace(q.Search); term != "" {
		like := "%" + strings.ToLower(term) + "%"
		conds = append(conds, fmt.Sprintf("(LOWER(path) LIKE %s OR LOWER(action) LIKE %s OR LOWER(method) LIKE %s OR LOWER(event_type) LIKE %s)",
			ph(like), ph(like), ph(like), ph(like)))
	}
	if q.Method != "" {
		conds = append(conds, "method = "+ph(strings.ToUpper(q.Method)))
	}
	if q.Category != "" {
		conds = append(conds, "category = "+ph(q.Category))
	}
	if q.Severity != "" {
		conds = append(conds, "severity = "+ph(q.Severity))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := s.db.GetContext(ctx, &total, "SELECT COUNT(*) FROM audit_log"+where, args...); err != nil {
		return nil, 0, err
	}

	sql := "SELECT * FROM audit_log" + where + auditOrderClause(q) + " LIMIT " + ph(auditLimit(q)) + " OFFSET " + ph(q.Offset)
	var es []models.AuditLogEntry
	err := s.db.SelectContext(ctx, &es, sql, args...)
	return es, total, err
}
