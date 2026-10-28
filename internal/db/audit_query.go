package db

import (
	"context"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/jmoiron/sqlx"
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
		return " ORDER BY created_at " + dir + ", id " + dir
	}
	// Tiebreak by time so paging is stable when sorting by a non-unique column.
	return " ORDER BY " + col + " " + dir + ", created_at DESC, id DESC"
}

func auditLimit(q AuditQuery) int {
	if q.Limit <= 0 || q.Limit > 1000 {
		return 100
	}
	return q.Limit
}

func (s *PostgresStore) QueryAuditLog(ctx context.Context, q AuditQuery) ([]models.AuditLogEntry, int, error) {
	return queryAuditLog(ctx, s.db, q)
}
func queryAuditLog(ctx context.Context, database *sqlx.DB, q AuditQuery) ([]models.AuditLogEntry, int, error) {
	var conds []string
	var args []any
	if term := strings.TrimSpace(q.Search); term != "" {
		like := "%" + strings.ToLower(term) + "%"
		conds = append(conds, "(LOWER(path) LIKE ? OR LOWER(action) LIKE ? OR LOWER(method) LIKE ? OR LOWER(event_type) LIKE ? OR LOWER(user_id) LIKE ? OR LOWER(token_id) LIKE ? OR LOWER(ip) LIKE ?)")
		args = append(args, like, like, like, like, like, like, like)
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

	if q.Event != "" {
		conds = append(conds, "event_type = ?")
		args = append(args, q.Event)
	}
	if q.Actor != "" {
		conds = append(conds, "(user_id = ? OR token_id = ?)")
		args = append(args, q.Actor, q.Actor)
	}
	if q.IP != "" {
		conds = append(conds, "ip = ?")
		args = append(args, q.IP)
	}
	if q.From != nil {
		conds = append(conds, "created_at >= ?")
		args = append(args, *q.From)
	}
	if q.Before != nil {
		conds = append(conds, "created_at < ?")
		args = append(args, *q.Before)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := database.GetContext(ctx, &total, database.Rebind("SELECT COUNT(*) FROM audit_log"+where), args...); err != nil {
		return nil, 0, err
	}

	sql := "SELECT * FROM audit_log" + where + auditOrderClause(q) + " LIMIT ? OFFSET ?"
	pageArgs := append(append([]any{}, args...), auditLimit(q), max(0, q.Offset))
	var es []models.AuditLogEntry
	err := database.SelectContext(ctx, &es, database.Rebind(sql), pageArgs...)
	return es, total, err
}
