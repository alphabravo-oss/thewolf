package db

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// findingIdent is the same issue across rescans and branches: file + line +
// rule. It must not use the semantic/stable hash — that ignores line number
// and collapses hundreds of yamllint hits in one file into a single count.
func findingIdent(f models.Finding) string {
	if s := strings.TrimSpace(f.LocationFingerprint); s != "" {
		return s
	}
	if s := strings.TrimSpace(f.FilePath); s != "" || strings.TrimSpace(f.RuleID) != "" {
		return strings.ToLower(strings.TrimSpace(f.ToolName) + "\x00" + strings.TrimSpace(f.RuleID) + "\x00" + strings.TrimSpace(f.FilePath) + "\x00" + fmt.Sprintf("%d", f.LineStart))
	}
	if s := strings.TrimSpace(f.Fingerprint); s != "" {
		return s
	}
	return f.ID
}

func identKey(repoID, ident string) string {
	return repoID + "\x00" + ident
}

func severityRankName(s models.Severity) int {
	switch models.Severity(strings.ToLower(string(s))) {
	case models.SeverityCritical:
		return 5
	case models.SeverityHigh:
		return 4
	case models.SeverityMedium:
		return 3
	case models.SeverityLow:
		return 2
	default:
		return 1
	}
}

func dedupCurrentFindings(in []models.Finding) []models.Finding {
	best := make(map[string]models.Finding, len(in))
	for _, f := range in {
		k := identKey(f.RepoID, findingIdent(f))
		prev, ok := best[k]
		if !ok {
			best[k] = f
			continue
		}
		pr, nr := severityRankName(prev.Severity), severityRankName(f.Severity)
		if nr > pr || (nr == pr && f.CreatedAt.After(prev.CreatedAt)) {
			best[k] = f
		}
	}
	out := make([]models.Finding, 0, len(best))
	for _, f := range best {
		out = append(out, f)
	}
	return out
}

func appendRepoScope(col string, fleetMode bool, collectionID, userID string, args []any) (extra string, out []any) {
	out = args
	if !fleetMode {
		out = append(out, userID)
		extra += ` AND ` + col + ` IN (SELECT id FROM repos WHERE user_id = ?)`
	}
	if collectionID != "" {
		out = append(out, collectionID)
		extra += ` AND ` + col + ` IN (SELECT repo_id FROM collection_repos WHERE collection_id = ?)`
	}
	return extra, out
}

func latestBranchScanIDs(ctx context.Context, db *sqlx.DB, userID string, fleetMode bool, collectionID string) ([]string, error) {
	q := `
SELECT s.id
FROM scans s
INNER JOIN (
  SELECT repo_id,
         COALESCE(NULLIF(branch, ''), '_') AS branch_key,
         MAX(COALESCE(completed_at, created_at)) AS latest_at
  FROM scans
  WHERE status IN ('completed', 'cancelled')
    AND TRIM(COALESCE(origin_scan_id, '')) = ''
  GROUP BY repo_id, COALESCE(NULLIF(branch, ''), '_')
) t ON t.repo_id = s.repo_id
   AND COALESCE(NULLIF(s.branch, ''), '_') = t.branch_key
   AND COALESCE(s.completed_at, s.created_at) = t.latest_at
WHERE s.status IN ('completed', 'cancelled')
  AND TRIM(COALESCE(origin_scan_id, '')) = ''`
	args := []any{}
	extra, args := appendRepoScope("s.repo_id", fleetMode, collectionID, userID, args)
	q += extra
	q = db.Rebind(q)
	var ids []string
	if err := db.SelectContext(ctx, &ids, q, args...); err != nil {
		return nil, fmt.Errorf("latest branch scans: %w", err)
	}
	return ids, nil
}

func listCurrentOpenFindings(ctx context.Context, db *sqlx.DB, userID string, fleetMode bool, collectionID string) ([]models.Finding, error) {
	ids, err := latestBranchScanIDs(ctx, db, userID, fleetMode, collectionID)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	q, args, err := sqlx.In(`SELECT * FROM findings WHERE status = 'open' AND scan_id IN (?)`, ids)
	if err != nil {
		return nil, err
	}
	q = db.Rebind(q)
	var findings []models.Finding
	if err := db.SelectContext(ctx, &findings, q, args...); err != nil {
		return nil, fmt.Errorf("current open findings: %w", err)
	}
	hydrateFindingsAfterRead(findings)
	return dedupCurrentFindings(findings), nil
}

func firstSeenByIdent(ctx context.Context, db *sqlx.DB, userID string, fleetMode bool, collectionID string) (map[string]time.Time, error) {
	q := `
SELECT repo_id,
       COALESCE(NULLIF(location_fingerprint, ''), id) AS ident,
       MIN(created_at) AS first_seen
FROM findings
WHERE 1=1`
	args := []any{}
	extra, args := appendRepoScope("repo_id", fleetMode, collectionID, userID, args)
	q += extra + ` GROUP BY repo_id, COALESCE(NULLIF(location_fingerprint, ''), id)`
	q = db.Rebind(q)
	type row struct {
		RepoID    string `db:"repo_id"`
		Ident     string `db:"ident"`
		FirstSeen string `db:"first_seen"`
	}
	var rows []row
	if err := db.SelectContext(ctx, &rows, q, args...); err != nil {
		return nil, fmt.Errorf("first seen: %w", err)
	}
	out := make(map[string]time.Time, len(rows))
	for _, r := range rows {
		if ts, ok := parseDBTime(r.FirstSeen); ok {
			out[identKey(r.RepoID, r.Ident)] = ts
		}
	}
	return out, nil
}

func parseDBTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05",
	} {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts.UTC(), true
		}
	}
	return time.Time{}, false
}

func countBySeverity(findings []models.Finding) map[string]int {
	out := map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0, "info": 0}
	for _, f := range findings {
		sev := strings.ToLower(string(f.Severity))
		if _, ok := out[sev]; ok {
			out[sev]++
		}
	}
	return out
}

func computeFleetPosture(ctx context.Context, database *sqlx.DB, userID string, fleetMode bool, collectionID string) (*FleetPostureResult, error) {
	out := &FleetPostureResult{
		OpenFindingsBySeverity: map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0, "info": 0},
		WeekOverWeekDelta:      map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0, "info": 0},
	}
	findings, err := listCurrentOpenFindings(ctx, database, userID, fleetMode, collectionID)
	if err != nil {
		return nil, err
	}
	out.OpenFindingsBySeverity = countBySeverity(findings)

	firstSeen, err := firstSeenByIdent(ctx, database, userID, fleetMode, collectionID)
	if err != nil {
		return nil, err
	}
	weekAgo := time.Now().AddDate(0, 0, -7).UTC()
	twoWeeksAgo := time.Now().AddDate(0, 0, -14).UTC()
	recent := map[string]int{}
	prior := map[string]int{}
	for _, f := range findings {
		seen, ok := firstSeen[identKey(f.RepoID, findingIdent(f))]
		if !ok {
			seen = f.CreatedAt
		}
		sev := strings.ToLower(string(f.Severity))
		if seen.After(weekAgo) || seen.Equal(weekAgo) {
			recent[sev]++
		} else if (seen.After(twoWeeksAgo) || seen.Equal(twoWeeksAgo)) && seen.Before(weekAgo) {
			prior[sev]++
		}
	}
	for sev := range out.WeekOverWeekDelta {
		out.WeekOverWeekDelta[sev] = recent[sev] - prior[sev]
	}

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
	repoQ = database.Rebind(repoQ)
	if err := database.QueryRowContext(ctx, repoQ, repoArgs...).Scan(&out.RepoCount); err != nil {
		return nil, fmt.Errorf("count repos: %w", err)
	}

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
	gateQ = database.Rebind(gateQ)
	_ = database.QueryRowContext(ctx, gateQ, gateArgs...).Scan(&out.GatesFailing)
	return out, nil
}

func computeNeedsAttention(ctx context.Context, database *sqlx.DB, userID string, fleetMode bool, limit int) ([]NeedsAttentionRow, error) {
	if limit <= 0 {
		limit = 10
	}
	repoQ := `SELECT id, name FROM repos`
	repoArgs := []any{}
	if !fleetMode {
		repoQ += ` WHERE user_id = ?`
		repoArgs = append(repoArgs, userID)
	}
	repoQ = database.Rebind(repoQ)
	type repoRef struct {
		ID, Name string
	}
	var repos []repoRef
	rows, err := database.QueryContext(ctx, repoQ, repoArgs...)
	if err != nil {
		return nil, fmt.Errorf("list repos for needs-attention: %w", err)
	}
	for rows.Next() {
		var r repoRef
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			rows.Close()
			return nil, err
		}
		repos = append(repos, r)
	}
	rows.Close()

	findings, err := listCurrentOpenFindings(ctx, database, userID, fleetMode, "")
	if err != nil {
		return nil, err
	}
	firstSeen, err := firstSeenByIdent(ctx, database, userID, fleetMode, "")
	if err != nil {
		return nil, err
	}
	weekAgo := time.Now().AddDate(0, 0, -7).UTC()
	type tallies struct{ crit, high int }
	byRepo := map[string]tallies{}
	for _, f := range findings {
		seen, ok := firstSeen[identKey(f.RepoID, findingIdent(f))]
		if !ok {
			seen = f.CreatedAt
		}
		if seen.Before(weekAgo) {
			continue
		}
		t := byRepo[f.RepoID]
		switch models.Severity(strings.ToLower(string(f.Severity))) {
		case models.SeverityCritical:
			t.crit++
		case models.SeverityHigh:
			t.high++
		}
		byRepo[f.RepoID] = t
	}

	out := make([]NeedsAttentionRow, 0, len(repos))
	for _, r := range repos {
		newCrit := byRepo[r.ID].crit
		newHigh := byRepo[r.ID].high

		gateFailing := 0
		var lastScanAt *time.Time
		lastQ := database.Rebind(`SELECT created_at FROM scans WHERE repo_id = ? ORDER BY created_at DESC LIMIT 1`)
		_ = database.QueryRowContext(ctx, lastQ, r.ID).Scan(&lastScanAt)
		gateQ := database.Rebind(`SELECT COUNT(*) FROM quality_gate_results g
			 JOIN scans s ON s.id = g.scan_id
			 WHERE s.repo_id = ? AND g.status = 'fail'
			 AND g.scan_id = (SELECT id FROM scans WHERE repo_id = ? ORDER BY created_at DESC LIMIT 1)`)
		_ = database.QueryRowContext(ctx, gateQ, r.ID, r.ID).Scan(&gateFailing)

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

func computeAggregateByRule(ctx context.Context, database *sqlx.DB, userID string, fleetMode bool, limit int) ([]FindingsAggregateRow, error) {
	if limit <= 0 {
		limit = 10
	}
	findings, err := listCurrentOpenFindings(ctx, database, userID, fleetMode, "")
	if err != nil {
		return nil, err
	}
	type agg struct {
		repos    map[string]bool
		findings int
		tool     string
		title    string
		severity models.Severity
	}
	byRule := map[string]*agg{}
	for _, f := range findings {
		rule := strings.TrimSpace(f.RuleID)
		if rule == "" {
			rule = strings.TrimSpace(f.Title)
		}
		if rule == "" {
			continue
		}
		a := byRule[rule]
		if a == nil {
			a = &agg{repos: map[string]bool{}, tool: f.ToolName, title: f.Title, severity: f.Severity}
			byRule[rule] = a
		}
		a.repos[f.RepoID] = true
		a.findings++
		if severityRankName(f.Severity) > severityRankName(a.severity) {
			a.severity = f.Severity
			if f.Title != "" {
				a.title = f.Title
			}
			if f.ToolName != "" {
				a.tool = f.ToolName
			}
		}
	}
	names := repoNames(ctx, database, userID, fleetMode)
	out := make([]FindingsAggregateRow, 0, len(byRule))
	for rule, a := range byRule {
		type pair struct{ id, name string }
		pairs := make([]pair, 0, len(a.repos))
		for id := range a.repos {
			name := names[id]
			if name == "" {
				name = id
				if len(name) > 8 {
					name = name[:8]
				}
			}
			pairs = append(pairs, pair{id: id, name: name})
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].name < pairs[j].name })
		ids := make([]string, len(pairs))
		label := make([]string, len(pairs))
		for i, p := range pairs {
			ids[i] = p.id
			label[i] = p.name
		}
		out = append(out, FindingsAggregateRow{
			Key: rule, Repos: len(a.repos), Findings: a.findings,
			Tool: a.tool, Title: a.title, Severity: string(a.severity),
			RepoIDs: ids, RepoNames: label,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Repos != out[j].Repos {
			return out[i].Repos > out[j].Repos
		}
		if out[i].Findings != out[j].Findings {
			return out[i].Findings > out[j].Findings
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *SQLiteStore) ListCurrentOpenFindings(ctx context.Context, userID string, fleetMode bool, collectionID string) ([]models.Finding, error) {
	return listCurrentOpenFindings(ctx, s.db, userID, fleetMode, collectionID)
}

func (s *SQLiteStore) FleetPosture(ctx context.Context, userID string, fleetMode bool, collectionID string) (*FleetPostureResult, error) {
	return computeFleetPosture(ctx, s.db, userID, fleetMode, collectionID)
}

func (s *SQLiteStore) FleetNeedsAttention(ctx context.Context, userID string, fleetMode bool, limit int) ([]NeedsAttentionRow, error) {
	return computeNeedsAttention(ctx, s.db, userID, fleetMode, limit)
}

func (s *SQLiteStore) FindingsAggregateByRule(ctx context.Context, userID string, fleetMode bool, limit int) ([]FindingsAggregateRow, error) {
	return computeAggregateByRule(ctx, s.db, userID, fleetMode, limit)
}

func (s *SQLiteStore) FindingsByRepo(ctx context.Context, userID string, fleetMode bool, collectionID string) ([]FindingsByRepoRow, error) {
	return computeFindingsByRepo(ctx, s.db, userID, fleetMode, collectionID)
}

func (s *PostgresStore) ListCurrentOpenFindings(ctx context.Context, userID string, fleetMode bool, collectionID string) ([]models.Finding, error) {
	return listCurrentOpenFindings(ctx, s.db, userID, fleetMode, collectionID)
}

func (s *PostgresStore) FleetPosture(ctx context.Context, userID string, fleetMode bool, collectionID string) (*FleetPostureResult, error) {
	return computeFleetPosture(ctx, s.db, userID, fleetMode, collectionID)
}

func (s *PostgresStore) FleetNeedsAttention(ctx context.Context, userID string, fleetMode bool, limit int) ([]NeedsAttentionRow, error) {
	return computeNeedsAttention(ctx, s.db, userID, fleetMode, limit)
}

func (s *PostgresStore) FindingsAggregateByRule(ctx context.Context, userID string, fleetMode bool, limit int) ([]FindingsAggregateRow, error) {
	return computeAggregateByRule(ctx, s.db, userID, fleetMode, limit)
}

func (s *PostgresStore) FindingsByRepo(ctx context.Context, userID string, fleetMode bool, collectionID string) ([]FindingsByRepoRow, error) {
	return computeFindingsByRepo(ctx, s.db, userID, fleetMode, collectionID)
}

func repoNames(ctx context.Context, database *sqlx.DB, userID string, fleetMode bool) map[string]string {
	q := `SELECT id, name FROM repos WHERE 1=1`
	args := []any{}
	if !fleetMode {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	q = database.Rebind(q)
	type row struct {
		ID   string `db:"id"`
		Name string `db:"name"`
	}
	var rows []row
	if err := database.SelectContext(ctx, &rows, q, args...); err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out
}

func computeFindingsByRepo(ctx context.Context, database *sqlx.DB, userID string, fleetMode bool, collectionID string) ([]FindingsByRepoRow, error) {
	findings, err := listCurrentOpenFindings(ctx, database, userID, fleetMode, collectionID)
	if err != nil {
		return nil, err
	}
	names := repoNames(ctx, database, userID, fleetMode)
	type bucket struct {
		total, crit, high, med, low, info int
	}
	by := map[string]*bucket{}
	for _, f := range findings {
		b := by[f.RepoID]
		if b == nil {
			b = &bucket{}
			by[f.RepoID] = b
		}
		b.total++
		switch models.Severity(strings.ToLower(string(f.Severity))) {
		case models.SeverityCritical:
			b.crit++
		case models.SeverityHigh:
			b.high++
		case models.SeverityMedium:
			b.med++
		case models.SeverityLow:
			b.low++
		default:
			b.info++
		}
	}
	out := make([]FindingsByRepoRow, 0, len(by))
	for id, b := range by {
		name := names[id]
		if name == "" {
			name = id
			if len(name) > 8 {
				name = name[:8]
			}
		}
		out = append(out, FindingsByRepoRow{
			RepoID: id, Name: name, Total: b.total,
			Critical: b.crit, High: b.high, Medium: b.med, Low: b.low, Info: b.info,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		ai := out[i].Critical*10 + out[i].High
		aj := out[j].Critical*10 + out[j].High
		if ai != aj {
			return ai > aj
		}
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
