package db

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// FleetPosture aggregates fleet-wide posture for the dashboard. When fleetMode
// is false, queries are scoped to userID via the owning repo. When true, the
// query covers every repo in the system. When collectionID is non-empty, the
// query is further scoped to repos that belong to that collection.
func (s *SQLiteStore) FleetPosture(ctx context.Context, userID string, fleetMode bool, collectionID string) (*FleetPostureResult, error) {
	out := &FleetPostureResult{
		OpenFindingsBySeverity: map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0, "info": 0},
		WeekOverWeekDelta:      map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0, "info": 0},
	}

	// Open findings by severity.
	sevQ := `SELECT severity, COUNT(*) FROM findings f WHERE status = 'open'`
	sevArgs := []any{}
	if !fleetMode {
		sevQ += ` AND f.repo_id IN (SELECT id FROM repos WHERE user_id = ?)`
		sevArgs = append(sevArgs, userID)
	}
	if collectionID != "" {
		sevQ += ` AND f.repo_id IN (SELECT repo_id FROM collection_repos WHERE collection_id = ?)`
		sevArgs = append(sevArgs, collectionID)
	}
	sevQ += ` GROUP BY severity`
	rows, err := s.db.QueryContext(ctx, sevQ, sevArgs...)
	if err != nil {
		return nil, fmt.Errorf("query open findings by severity: %w", err)
	}
	for rows.Next() {
		var sev string
		var n int
		if err := rows.Scan(&sev, &n); err != nil {
			rows.Close()
			return nil, err
		}
		if _, ok := out.OpenFindingsBySeverity[sev]; ok {
			out.OpenFindingsBySeverity[sev] = n
		}
	}
	rows.Close()

	// Week-over-week delta: findings created in the last 7 days minus
	// findings created in the prior 7 days, by severity.
	weekAgo := time.Now().AddDate(0, 0, -7).UTC().Format(time.RFC3339)
	twoWeeksAgo := time.Now().AddDate(0, 0, -14).UTC().Format(time.RFC3339)

	recent := map[string]int{}
	prior := map[string]int{}

	recentQ := `SELECT severity, COUNT(*) FROM findings f WHERE created_at >= ?`
	recentArgs := []any{weekAgo}
	if !fleetMode {
		recentQ += ` AND f.repo_id IN (SELECT id FROM repos WHERE user_id = ?)`
		recentArgs = append(recentArgs, userID)
	}
	if collectionID != "" {
		recentQ += ` AND f.repo_id IN (SELECT repo_id FROM collection_repos WHERE collection_id = ?)`
		recentArgs = append(recentArgs, collectionID)
	}
	recentQ += ` GROUP BY severity`
	if rows, err := s.db.QueryContext(ctx, recentQ, recentArgs...); err == nil {
		for rows.Next() {
			var sev string
			var n int
			if err := rows.Scan(&sev, &n); err != nil {
				rows.Close()
				return nil, err
			}
			recent[sev] = n
		}
		rows.Close()
	}

	priorQ := `SELECT severity, COUNT(*) FROM findings f WHERE created_at >= ? AND created_at < ?`
	priorArgs := []any{twoWeeksAgo, weekAgo}
	if !fleetMode {
		priorQ += ` AND f.repo_id IN (SELECT id FROM repos WHERE user_id = ?)`
		priorArgs = append(priorArgs, userID)
	}
	if collectionID != "" {
		priorQ += ` AND f.repo_id IN (SELECT repo_id FROM collection_repos WHERE collection_id = ?)`
		priorArgs = append(priorArgs, collectionID)
	}
	priorQ += ` GROUP BY severity`
	if rows, err := s.db.QueryContext(ctx, priorQ, priorArgs...); err == nil {
		for rows.Next() {
			var sev string
			var n int
			if err := rows.Scan(&sev, &n); err != nil {
				rows.Close()
				return nil, err
			}
			prior[sev] = n
		}
		rows.Close()
	}

	for sev := range out.WeekOverWeekDelta {
		out.WeekOverWeekDelta[sev] = recent[sev] - prior[sev]
	}

	// Repo count.
	repoQ := `SELECT COUNT(*) FROM repos r WHERE 1=1`
	repoArgs := []any{}
	if !fleetMode {
		repoQ += ` AND r.user_id = ?`
		repoArgs = append(repoArgs, userID)
	}
	if collectionID != "" {
		repoQ += ` AND r.id IN (SELECT repo_id FROM collection_repos WHERE collection_id = ?)`
		repoArgs = append(repoArgs, collectionID)
	}
	if err := s.db.QueryRowContext(ctx, repoQ, repoArgs...).Scan(&out.RepoCount); err != nil {
		return nil, fmt.Errorf("count repos: %w", err)
	}

	// Gates failing: count of distinct repos whose most-recent gate result
	// for any scan is 'fail'.
	gateQ := `SELECT COUNT(DISTINCT s.repo_id)
	          FROM quality_gate_results g
	          JOIN scans s ON s.id = g.scan_id
	          WHERE g.status = 'fail'`
	gateArgs := []any{}
	if !fleetMode {
		gateQ += ` AND s.repo_id IN (SELECT id FROM repos WHERE user_id = ?)`
		gateArgs = append(gateArgs, userID)
	}
	if collectionID != "" {
		gateQ += ` AND s.repo_id IN (SELECT repo_id FROM collection_repos WHERE collection_id = ?)`
		gateArgs = append(gateArgs, collectionID)
	}
	// Tolerate the case where no rows match (Scan into out.GatesFailing
	// returns 0). Ignore errors here so a missing table never breaks the
	// posture call.
	_ = s.db.QueryRowContext(ctx, gateQ, gateArgs...).Scan(&out.GatesFailing)

	return out, nil
}

// FleetInventory returns counts of repos by source_type, by collection name,
// and by detected language.
func (s *SQLiteStore) FleetInventory(ctx context.Context, userID string, fleetMode bool) (*FleetInventoryResult, error) {
	out := &FleetInventoryResult{
		BySourceType: map[string]int{},
		ByCollection: map[string]int{},
		ByLanguage:   map[string]int{},
	}

	// By source type.
	srcQ := `SELECT source_type, COUNT(*) FROM repos`
	srcArgs := []any{}
	if !fleetMode {
		srcQ += ` WHERE user_id = ?`
		srcArgs = append(srcArgs, userID)
	}
	srcQ += ` GROUP BY source_type`
	rows, err := s.db.QueryContext(ctx, srcQ, srcArgs...)
	if err != nil {
		return nil, fmt.Errorf("inventory by source_type: %w", err)
	}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			rows.Close()
			return nil, err
		}
		out.BySourceType[st] = n
	}
	rows.Close()

	// By collection (name).
	colQ := `SELECT c.name, COUNT(cr.repo_id)
	         FROM collections c
	         LEFT JOIN collection_repos cr ON cr.collection_id = c.id`
	colArgs := []any{}
	if !fleetMode {
		colQ += ` WHERE c.user_id = ?`
		colArgs = append(colArgs, userID)
	}
	colQ += ` GROUP BY c.id, c.name`
	if rows, err := s.db.QueryContext(ctx, colQ, colArgs...); err == nil {
		for rows.Next() {
			var name string
			var n int
			if err := rows.Scan(&name, &n); err != nil {
				rows.Close()
				return nil, err
			}
			out.ByCollection[name] = n
		}
		rows.Close()
	}

	// By language: detected_languages is a JSON array of strings. Decode in
	// Go since SQLite JSON support varies.
	langQ := `SELECT detected_languages FROM repos`
	langArgs := []any{}
	if !fleetMode {
		langQ += ` WHERE user_id = ?`
		langArgs = append(langArgs, userID)
	}
	if rows, err := s.db.QueryContext(ctx, langQ, langArgs...); err == nil {
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			for _, lang := range parseLanguageList(raw) {
				out.ByLanguage[lang]++
			}
		}
		rows.Close()
	}

	return out, nil
}

// FleetNeedsAttention returns the top-N repos sorted by a composite score:
//
//	score = 10*new_critical + 5*new_high + 8*(gate_failing) + 1*stale_days_over_30
func (s *SQLiteStore) FleetNeedsAttention(ctx context.Context, userID string, fleetMode bool, limit int) ([]NeedsAttentionRow, error) {
	if limit <= 0 {
		limit = 10
	}

	// 1. List candidate repos.
	repoQ := `SELECT id, name FROM repos`
	repoArgs := []any{}
	if !fleetMode {
		repoQ += ` WHERE user_id = ?`
		repoArgs = append(repoArgs, userID)
	}
	rows, err := s.db.QueryContext(ctx, repoQ, repoArgs...)
	if err != nil {
		return nil, fmt.Errorf("list repos for needs-attention: %w", err)
	}
	type repoRef struct {
		ID, Name string
	}
	var repos []repoRef
	for rows.Next() {
		var r repoRef
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			rows.Close()
			return nil, err
		}
		repos = append(repos, r)
	}
	rows.Close()

	out := make([]NeedsAttentionRow, 0, len(repos))
	weekAgo := time.Now().AddDate(0, 0, -7).UTC().Format(time.RFC3339)

	for _, r := range repos {
		newCrit := 0
		newHigh := 0
		_ = s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM findings WHERE repo_id = ? AND status = 'open' AND severity = 'critical' AND created_at >= ?`,
			r.ID, weekAgo).Scan(&newCrit)
		_ = s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM findings WHERE repo_id = ? AND status = 'open' AND severity = 'high' AND created_at >= ?`,
			r.ID, weekAgo).Scan(&newHigh)

		// Latest scan + gate status + staleness.
		gateFailing := 0
		var lastScanAt *time.Time
		_ = s.db.QueryRowContext(ctx,
			`SELECT created_at FROM scans WHERE repo_id = ? ORDER BY created_at DESC LIMIT 1`,
			r.ID).Scan(&lastScanAt)

		_ = s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM quality_gate_results g
			 JOIN scans s ON s.id = g.scan_id
			 WHERE s.repo_id = ? AND g.status = 'fail'
			 AND g.scan_id = (SELECT id FROM scans WHERE repo_id = ? ORDER BY created_at DESC LIMIT 1)`,
			r.ID, r.ID).Scan(&gateFailing)

		staleDaysOver30 := 0
		if lastScanAt != nil {
			d := int(time.Since(*lastScanAt).Hours() / 24)
			if d > 30 {
				staleDaysOver30 = d - 30
			}
		} else {
			staleDaysOver30 = 30
		}

		score := 10*newCrit + 5*newHigh + 8*gateFailing + staleDaysOver30
		if score == 0 {
			continue
		}

		reason := "stale"
		detail := ""
		switch {
		case newCrit > 0:
			reason = "new_high"
			detail = fmt.Sprintf("%d new critical, %d new high", newCrit, newHigh)
		case newHigh > 0:
			reason = "new_high"
			detail = fmt.Sprintf("%d new high", newHigh)
		case gateFailing > 0:
			reason = "gate_failing"
			detail = "latest scan failed gate"
		case lastScanAt == nil:
			reason = "stale"
			detail = "never scanned"
		default:
			reason = "stale"
			detail = fmt.Sprintf("last scan %d days ago", staleDaysOver30+30)
		}

		out = append(out, NeedsAttentionRow{
			RepoID: r.ID,
			Name:   r.Name,
			Reason: reason,
			Detail: detail,
			Score:  score,
		})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// FindingsAggregateByRule returns the top-N rule_ids by distinct repo count,
// then by total finding count. Empty rule_ids are excluded.
func (s *SQLiteStore) FindingsAggregateByRule(ctx context.Context, userID string, fleetMode bool, limit int) ([]FindingsAggregateRow, error) {
	if limit <= 0 {
		limit = 10
	}
	q := `SELECT rule_id, COUNT(DISTINCT repo_id) AS repos, COUNT(*) AS findings
	      FROM findings
	      WHERE status = 'open' AND rule_id IS NOT NULL AND rule_id != ''`
	args := []any{}
	if !fleetMode {
		q += ` AND repo_id IN (SELECT id FROM repos WHERE user_id = ?)`
		args = append(args, userID)
	}
	q += ` GROUP BY rule_id ORDER BY repos DESC, findings DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("findings aggregate: %w", err)
	}
	defer rows.Close()

	out := make([]FindingsAggregateRow, 0, limit)
	for rows.Next() {
		var row FindingsAggregateRow
		if err := rows.Scan(&row.Key, &row.Repos, &row.Findings); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// parseLanguageList decodes detected_languages, which may be a JSON array
// (`["go","ts"]`), a comma-separated list, or empty.
func parseLanguageList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "null" {
		return nil
	}
	// JSON array form.
	if strings.HasPrefix(raw, "[") {
		trimmed := strings.TrimPrefix(strings.TrimSuffix(raw, "]"), "[")
		parts := strings.Split(trimmed, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			p = strings.Trim(p, `"`)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	// Fallback: comma-separated.
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
