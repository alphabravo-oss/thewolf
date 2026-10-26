package db

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/alphabravocompany/thewolf/internal/finding/gates"
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
	best := make(map[string]int, len(in))
	for i := range in {
		f := &in[i]
		k := identKey(f.RepoID, findingIdent(*f))
		previous, ok := best[k]
		if !ok {
			best[k] = i
			continue
		}
		prev := &in[previous]
		pr, nr := severityRankName(prev.Severity), severityRankName(f.Severity)
		if nr > pr || (nr == pr && (f.CreatedAt.After(prev.CreatedAt) || (f.CreatedAt.Equal(prev.CreatedAt) && f.ID < prev.ID))) {
			best[k] = i
		}
	}
	out := make([]models.Finding, 0, len(best))
	for _, i := range best {
		out = append(out, in[i])
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

func listCurrentOpenFindings(ctx context.Context, database *sqlx.DB, userID string, fleetMode bool, collectionID string) ([]models.Finding, error) {
	e, err := loadFleetEvidence(ctx, database, userID, fleetMode, collectionID)
	if err != nil {
		return nil, err
	}
	current := e.currentEvidence(time.Now().UTC())
	// Keep suppressed open records for the include_suppressed API option.
	current = slices.DeleteFunc(current, func(f models.Finding) bool { return f.Status != models.StatusOpen })
	current = dedupCurrentFindings(current)
	// Aggregates read compact evidence; REST findings and vulnerability callers
	// still need complete records, including descriptions, scores and SARIF.
	findings := make([]models.Finding, 0, len(current))
	for batch := range slices.Chunk(current, 500) {
		ids := make([]string, len(batch))
		for i, f := range batch {
			ids[i] = f.ID
		}
		query, args, err := sqlx.In(`SELECT * FROM findings WHERE id IN (?)`, ids)
		if err != nil {
			return nil, err
		}
		var rows []models.Finding
		if err := database.SelectContext(ctx, &rows, database.Rebind(query), args...); err != nil {
			return nil, err
		}
		findings = append(findings, rows...)
	}
	hydrateFindingsAfterRead(findings)
	return findings, nil
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
	e, err := loadFleetEvidence(ctx, database, userID, fleetMode, collectionID)
	if err != nil {
		return nil, err
	}
	return e.posture(time.Now().UTC()), nil
}

func computeNeedsAttention(ctx context.Context, database *sqlx.DB, userID string, fleetMode bool, limit int) ([]NeedsAttentionRow, error) {
	e, err := loadFleetEvidence(ctx, database, userID, fleetMode, "")
	if err != nil {
		return nil, err
	}
	rows := e.attention(time.Now().UTC())
	if limit <= 0 {
		limit = 10
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func computeAggregateByRule(ctx context.Context, database *sqlx.DB, userID string, fleetMode bool, limit int, securityOnly bool) ([]FindingsAggregateRow, error) {
	if limit <= 0 {
		limit = 10
	}
	e, err := loadFleetEvidence(ctx, database, userID, fleetMode, "")
	if err != nil {
		return nil, err
	}
	findings := e.currentEvidence(time.Now().UTC())
	findings = slices.DeleteFunc(findings, func(f models.Finding) bool { return f.Status != models.StatusOpen })
	findings = dedupCurrentFindings(findings)
	findings = slices.DeleteFunc(findings, func(f models.Finding) bool { return f.Suppressed })
	type agg struct {
		repos    map[string]bool
		findings int
		tool     string
		title    string
		severity models.Severity
	}
	byRule := map[string]*agg{}
	for _, f := range findings {
		if securityOnly && !slices.Contains(gates.SecurityCategories(), f.Category) {
			continue
		}
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
		if securityOnly && out[i].Severity != out[j].Severity {
			return severityRankName(models.Severity(out[i].Severity)) > severityRankName(models.Severity(out[j].Severity))
		}
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

func (s *SQLiteStore) FindingsAggregateByRule(ctx context.Context, userID string, fleetMode bool, limit int, securityOnly bool) ([]FindingsAggregateRow, error) {
	return computeAggregateByRule(ctx, s.db, userID, fleetMode, limit, securityOnly)
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

func (s *PostgresStore) FindingsAggregateByRule(ctx context.Context, userID string, fleetMode bool, limit int, securityOnly bool) ([]FindingsAggregateRow, error) {
	return computeAggregateByRule(ctx, s.db, userID, fleetMode, limit, securityOnly)
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
	e, err := loadFleetEvidence(ctx, database, userID, fleetMode, collectionID)
	if err != nil {
		return nil, err
	}
	findings := e.currentEvidence(time.Now().UTC())
	findings = slices.DeleteFunc(findings, func(f models.Finding) bool { return f.Status != models.StatusOpen })
	findings = dedupCurrentFindings(findings)
	findings = slices.DeleteFunc(findings, func(f models.Finding) bool { return f.Suppressed })
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
