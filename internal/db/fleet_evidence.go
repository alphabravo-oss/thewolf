package db

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/trace"
	"slices"
	"sort"
	"strings"
	"time"

	findingdiff "github.com/alphabravocompany/thewolf/internal/finding/diff"
	"github.com/alphabravocompany/thewolf/internal/finding/gates"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/jmoiron/sqlx"
)

const fleetFreshDays = 7

type FleetCoverage struct {
	FreshDays  int                `json:"fresh_days"`
	FreshRepos int                `json:"fresh_repos"`
	Rows       []FleetCoverageRow `json:"rows"`
}

type FleetCoverageRow struct {
	RepoID         string     `json:"repo_id"`
	Name           string     `json:"name"`
	Branch         string     `json:"branch"`
	ScanID         string     `json:"scan_id,omitempty"`
	Status         string     `json:"status"`
	Detail         string     `json:"detail"`
	LastCompleteAt *time.Time `json:"last_complete_at,omitempty"`
	GateFailing    bool       `json:"gate_failing"`
}

type FleetChanges struct {
	WindowDays       int              `json:"window_days"`
	ComparedBranches int              `json:"compared_branches"`
	ExcludedBranches int              `json:"excluded_branches"`
	New              int              `json:"new"`
	Resurfaced       int              `json:"resurfaced"`
	Resolved         int              `json:"resolved"`
	Suppressed       int              `json:"suppressed"`
	Rows             []FleetChangeRow `json:"rows"`
}

type FleetChangeRow struct {
	RepoID         string `json:"repo_id"`
	Name           string `json:"name"`
	Branch         string `json:"branch"`
	ScanID         string `json:"scan_id,omitempty"`
	BaselineScanID string `json:"baseline_scan_id,omitempty"`
	Reason         string `json:"reason,omitempty"`
	New            int    `json:"new"`
	Resurfaced     int    `json:"resurfaced"`
	Resolved       int    `json:"resolved"`
	Suppressed     int    `json:"suppressed"`
}

type FleetPriority struct {
	FindingID  string          `json:"finding_id"`
	RepoID     string          `json:"repo_id"`
	Name       string          `json:"name"`
	Title      string          `json:"title"`
	Severity   models.Severity `json:"severity"`
	Tool       string          `json:"tool"`
	FilePath   string          `json:"file_path"`
	Line       int             `json:"line"`
	State      string          `json:"state"`
	Confidence string          `json:"confidence"`
}

type FleetScanWork struct {
	ScanID  string `json:"scan_id"`
	RepoID  string `json:"repo_id"`
	Name    string `json:"name"`
	Branch  string `json:"branch"`
	Status  string `json:"status"`
	Phase   string `json:"phase"`
	Stalled bool   `json:"stalled"`
}

type FleetTrendEntry struct {
	Date   string         `json:"date"`
	Counts map[string]int `json:"counts"`
	// Scope is explicit: these are deduplicated observations, not verified fixes.
	Repos           int `json:"repos"`
	IncompleteScans int `json:"incomplete_scans"`
}

type fleetRepo struct {
	ID   string `db:"id"`
	Name string `db:"name"`
}

// fleetFinding holds only the persisted columns read by fleet evidence. Keep full
// finding payloads in the paged/detail queries, and share evidence by pointer.
type fleetFinding struct {
	ID                  string          `db:"id"`
	ScanID              string          `db:"scan_id"`
	RepoID              string          `db:"repo_id"`
	Fingerprint         string          `db:"fingerprint"`
	StableFingerprint   string          `db:"stable_fingerprint"`
	LocationFingerprint string          `db:"location_fingerprint"`
	ToolName            string          `db:"tool_name"`
	Category            models.Category `db:"category"`
	Severity            models.Severity `db:"severity"`
	Title               string          `db:"title"`
	FilePath            string          `db:"file_path"`
	LineStart           int             `db:"line_start"`
	RuleID              string          `db:"rule_id"`
	Status              models.Status   `db:"status"`
	Suppressed          bool            `db:"suppressed"`
	BaselineState       string          `db:"baseline_state"`
	Confidence          string          `db:"confidence"`
	CreatedAt           time.Time       `db:"created_at"`
}

func (f *fleetFinding) diffKey() string {
	// Match the legacy projection passed to diff.Compare, including its identity
	// fallback when neither fingerprint was persisted.
	return findingdiff.Key(models.Finding{
		Fingerprint: f.Fingerprint, StableFingerprint: f.StableFingerprint,
		ToolName: f.ToolName, Category: f.Category, Title: f.Title,
		FilePath: f.FilePath, LineStart: f.LineStart, RuleID: f.RuleID,
	})
}

type fleetEvidence struct {
	repos       []fleetRepo
	scans       []models.Scan
	findings    map[string][]*fleetFinding
	failedGates map[string]bool
}

// ponytail: O(scan + finding history) per aggregate, using four scoped queries
// and compact evidence columns (no SARIF/snippets). Materialize daily snapshots
// when history volume makes this too expensive; never cap history silently.
func loadFleetEvidence(ctx context.Context, database sqlx.ExtContext, userID string, fleetMode bool, collectionID, repoID string) (*fleetEvidence, error) {
	region := trace.StartRegion(ctx, "finding.database")
	defer region.End()
	e := &fleetEvidence{findings: map[string][]*fleetFinding{}, failedGates: map[string]bool{}}
	scope, args := appendRepoScope("r.id", fleetMode, collectionID, userID, nil)
	if repoID != "" {
		scope += " AND r.id = ?"
		args = append(args, repoID)
	}
	if err := sqlx.SelectContext(ctx, database, &e.repos, database.Rebind(`SELECT r.id, r.name FROM repos r WHERE 1=1`+scope+` ORDER BY r.name, r.id`), args...); err != nil {
		return nil, err
	}
	scope, args = appendRepoScope("s.repo_id", fleetMode, collectionID, userID, nil)
	if repoID != "" {
		scope += " AND s.repo_id = ?"
		args = append(args, repoID)
	}
	root := ` FROM scans s JOIN repos r ON r.id = s.repo_id WHERE TRIM(COALESCE(s.origin_scan_id, '')) = ''` + scope
	if err := sqlx.SelectContext(ctx, database, &e.scans, database.Rebind(`SELECT s.id, s.repo_id, s.branch, s.source_type, s.source_path, s.remote_node_id, s.profile, s.categories, s.include_paths, s.exclude_paths, s.status, s.tools_selected, s.tools_completed, s.tools_failed, s.scanner_release_id, s.release_manifest_digest, s.created_at, s.completed_at, s.phase, s.lease_expires_at, s.heartbeat_at`+root+` ORDER BY s.created_at, s.id`), args...); err != nil {
		return nil, err
	}
	rows, err := database.QueryxContext(ctx, database.Rebind(`SELECT f.id, f.scan_id, f.repo_id, f.fingerprint, f.stable_fingerprint, f.location_fingerprint, f.tool_name, f.category, f.severity, f.title, f.file_path, f.line_start, f.rule_id, f.status, f.suppressed, f.baseline_state, f.confidence, f.created_at FROM findings f WHERE (f.cluster_id = '' OR f.cluster_id = f.id) AND f.scan_id IN (SELECT s.id`+root+`) ORDER BY f.scan_id, f.id`), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	// Repeated labels and paths dominate retained strings in large histories.
	// Intern only these low-cardinality fields, within this request's snapshot.
	labels := map[string]string{}
	intern := func(value string) string {
		if existing, ok := labels[value]; ok {
			return existing
		}
		labels[value] = value
		return value
	}
	var findings []*fleetFinding
	for rows.Next() {
		f := new(fleetFinding)
		if err := rows.StructScan(f); err != nil {
			return nil, err
		}
		f.ScanID, f.RepoID = intern(f.ScanID), intern(f.RepoID)
		f.ToolName, f.RuleID = intern(f.ToolName), intern(f.RuleID)
		f.Title, f.FilePath = intern(f.Title), intern(f.FilePath)
		f.Category, f.Severity = models.Category(intern(string(f.Category))), models.Severity(intern(string(f.Severity)))
		f.Status = models.Status(intern(string(f.Status)))
		f.BaselineState, f.Confidence = intern(f.BaselineState), intern(f.Confidence)
		findings = append(findings, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// Group pointer views into the same backing array; never copy evidence.
	for start := 0; start < len(findings); {
		end := start + 1
		for end < len(findings) && findings[end].ScanID == findings[start].ScanID {
			end++
		}
		e.findings[findings[start].ScanID] = findings[start:end:end]
		start = end
	}
	var failed []string
	if err := sqlx.SelectContext(ctx, database, &failed, database.Rebind(`SELECT DISTINCT g.scan_id FROM quality_gate_results g WHERE g.status = 'fail' AND g.scan_id IN (SELECT s.id`+root+`)`), args...); err != nil {
		return nil, err
	}
	for _, id := range failed {
		e.failedGates[id] = true
	}
	return e, nil
}

func scanObservedAt(s models.Scan) time.Time {
	if s.CompletedAt != nil {
		return s.CompletedAt.UTC()
	}
	return s.CreatedAt.UTC()
}
func completeEvidence(s models.Scan) bool {
	return gates.EvaluateScan(gates.Policy{}, &s, nil).Status == gates.StatusPass
}
func branchKey(s models.Scan) string    { return identKey(s.RepoID, s.Branch) }
func openEvidence(f *fleetFinding) bool { return f.Status == models.StatusOpen && !f.Suppressed }
func securityEvidence(f *fleetFinding) bool {
	return slices.Contains(gates.SecurityCategories(), f.Category)
}
func progressKey(f *fleetFinding) string {
	// Use the same durable identity as scan comparisons so line movement cannot
	// claim a fix. Current backlog still counts individual source locations.
	key := f.StableFingerprint
	if key == "" {
		key = f.Fingerprint
	}
	if key == "" {
		key = findingIdent(f)
	}
	return identKey(f.RepoID, key)
}

func (e *fleetEvidence) currentEvidence(at time.Time) []*fleetFinding {
	branches := map[string]map[string]*fleetFinding{}
	for _, s := range e.scans {
		if scanObservedAt(s).After(at) {
			continue
		}
		key := branchKey(s)
		if branches[key] == nil || completeEvidence(s) {
			branches[key] = make(map[string]*fleetFinding, len(e.findings[s.ID]))
		}
		// Incomplete/cancelled attempts may add evidence, never erase the last
		// complete observation. A repeated finding's latest triage state wins.
		for i := range e.findings[s.ID] {
			f := e.findings[s.ID][i]
			branches[key][findingIdent(f)] = f
		}
	}
	count := 0
	for _, branch := range branches {
		count += len(branch)
	}
	findings := make([]*fleetFinding, 0, count)
	for _, branch := range branches {
		for _, f := range branch {
			findings = append(findings, f)
		}
	}
	return findings
}

func (e *fleetEvidence) currentOpen(at time.Time) []*fleetFinding {
	findings := e.currentEvidence(at)
	findings = slices.DeleteFunc(findings, func(f *fleetFinding) bool { return !openEvidence(f) })
	return dedupCurrentFindings(findings)
}

func (e *fleetEvidence) coverage(now time.Time) FleetCoverage {
	out := FleetCoverage{FreshDays: fleetFreshDays, Rows: []FleetCoverageRow{}}
	latest := map[string]models.Scan{}
	completed := map[string]time.Time{}
	for _, s := range e.scans {
		key := branchKey(s)
		// Latest attempt is creation order, not finish order; an old, slow scan
		// cannot conceal a newer failed attempt.
		prev, ok := latest[key]
		if !ok || s.CreatedAt.After(prev.CreatedAt) || (s.CreatedAt.Equal(prev.CreatedAt) && s.ID > prev.ID) {
			latest[key] = s
		}
		if completeEvidence(s) {
			completed[key] = scanObservedAt(s)
		}
	}
	for _, repo := range e.repos {
		seen, fresh := false, true
		for key, s := range latest {
			if s.RepoID != repo.ID {
				continue
			}
			seen = true
			row := FleetCoverageRow{RepoID: repo.ID, Name: repo.Name, Branch: s.Branch, ScanID: s.ID, GateFailing: e.failedGates[s.ID]}
			if at, ok := completed[key]; ok {
				row.LastCompleteAt = &at
			}
			switch {
			case s.Status == models.ScanStatusPending || s.Status == models.ScanStatusRunning:
				row.Status, row.Detail = string(s.Status), "Latest scan has not finished."
			case s.Status == models.ScanStatusFailed:
				row.Status, row.Detail = "failed", "Latest scan failed; coverage is unverified."
			case !completeEvidence(s):
				row.Status, row.Detail = "incomplete", gates.EvaluateScan(gates.Policy{}, &s, nil).Reason
			case scanObservedAt(s).Before(now.AddDate(0, 0, -fleetFreshDays)):
				row.Status, row.Detail = "stale", "No complete scan in the last 7 days."
			default:
				row.Status, row.Detail = "fresh", "All selected tools completed within 7 days."
			}
			if row.Status != "fresh" {
				fresh = false
			}
			out.Rows = append(out.Rows, row)
		}
		if !seen {
			fresh = false
			out.Rows = append(out.Rows, FleetCoverageRow{RepoID: repo.ID, Name: repo.Name, Status: "never_scanned", Detail: "Run the first scan to establish coverage."})
		}
		if fresh {
			out.FreshRepos++
		}
	}
	sort.Slice(out.Rows, func(i, j int) bool {
		a, b := out.Rows[i], out.Rows[j]
		if (a.Status == "fresh") != (b.Status == "fresh") {
			return a.Status != "fresh"
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.RepoID != b.RepoID {
			return a.RepoID < b.RepoID
		}
		return a.Branch < b.Branch
	})
	return out
}

func scanScope(s models.Scan) string {
	// Empty optional lists and [] mean the same thing; malformed input is never
	// comparable. Tools/order alone must not make an identical scope different.
	lists := []string{s.ToolsSelected, s.Categories, s.IncludePaths, s.ExcludePaths}
	for i, raw := range lists {
		var values []string
		if strings.TrimSpace(raw) != "" && json.Unmarshal([]byte(raw), &values) != nil {
			return ""
		}
		slices.Sort(values)
		lists[i] = strings.Join(slices.Compact(values), "\x00")
	}
	node := ""
	if s.RemoteNodeID != nil {
		node = *s.RemoteNodeID
	}
	raw, _ := json.Marshal([]any{s.SourceType, s.SourcePath, node, s.Profile, lists, s.ScannerReleaseID, s.ReleaseManifestDigest})
	return string(raw)
}

func distinctProgress(findings []*fleetFinding) []*fleetFinding {
	byKey := map[string]*fleetFinding{}
	for _, f := range findings {
		key := progressKey(f)
		previous, exists := byKey[key]
		if !exists || (openEvidence(f) && !openEvidence(previous)) || (openEvidence(f) == openEvidence(previous) && severityRankName(f.Severity) > severityRankName(previous.Severity)) {
			byKey[key] = f
		}
	}
	out := make([]*fleetFinding, 0, len(byKey))
	for _, f := range byKey {
		out = append(out, f)
	}
	return out
}

func (e *fleetEvidence) changes(now time.Time, coverage FleetCoverage) (FleetChanges, map[string]int) {
	out := FleetChanges{WindowDays: 7, Rows: []FleetChangeRow{}}
	cutoff := now.AddDate(0, 0, -7)
	byID, bases := map[string]models.Scan{}, map[string]models.Scan{}
	for _, s := range e.scans {
		byID[s.ID] = s
		if !scanObservedAt(s).After(cutoff) {
			if completeEvidence(s) {
				bases[branchKey(s)] = s
			}
		}
	}
	comparable := map[string]bool{}
	for _, cov := range coverage.Rows {
		row := FleetChangeRow{RepoID: cov.RepoID, Name: cov.Name, Branch: cov.Branch, ScanID: cov.ScanID}
		cur := byID[cov.ScanID]
		base, hasBase := bases[branchKey(cur)]
		row.BaselineScanID = base.ID
		switch {
		case cov.Status != "fresh":
			row.Reason = "Needs a recent, complete scan."
		case !hasBase:
			row.Reason = "No complete baseline from before this seven-day window."
		case scanObservedAt(base).Before(cutoff.AddDate(0, 0, -30)):
			row.Reason = "Baseline is over 30 days older than the window."
		case cur.ReleaseManifestDigest == "" && cur.ScannerReleaseID == "":
			row.Reason = "Scanner release was not recorded."
		case scanScope(cur) == "" || scanScope(cur) != scanScope(base):
			row.Reason = "Tools, scan scope, or scanner release changed."
		}
		if row.Reason == "" {
			comparable[branchKey(cur)] = true
			out.ComparedBranches++
		} else {
			out.ExcludedBranches++
		}
		out.Rows = append(out.Rows, row)
	}
	if out.ComparedBranches == 0 {
		return out, countBySeverity(nil)
	}
	historical := map[string]bool{}
	for _, s := range e.scans {
		if !scanObservedAt(s).After(cutoff) {
			for _, f := range e.findings[s.ID] {
				historical[progressKey(f)] = true
			}
		}
	}
	currentKeys, baselineOpen, unverified := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, f := range e.currentEvidence(now) {
		currentKeys[progressKey(f)] = true
	}
	for _, f := range e.currentEvidence(cutoff) {
		key := progressKey(f)
		if openEvidence(f) {
			baselineOpen[key] = true
		}
		// If an affected branch could not be compared, absence on another branch
		// cannot establish that this repository's issue was resolved.
		if !comparable[branchKey(byID[f.ScanID])] {
			unverified[key] = true
		}
	}
	newKeys, resurfaced, resolved, suppressed := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	var beforeLocations, afterLocations []*fleetFinding
	for i := range out.Rows {
		row := &out.Rows[i]
		if row.Reason != "" {
			continue
		}
		before, after := distinctProgress(e.findings[row.BaselineScanID]), distinctProgress(e.findings[row.ScanID])
		// Include suppressed observations in the diff: hiding an issue cannot
		// masquerade as its absence in a verification scan.
		baseByID := make(map[string]*fleetFinding, len(before))
		for _, f := range before {
			key := f.diffKey()
			if _, exists := baseByID[key]; key != "" && !exists {
				baseByID[key] = f
			}
		}
		seen := make(map[string]bool, len(after))
		for _, f := range after {
			id := f.diffKey()
			seen[id] = true
			if _, exists := baseByID[id]; exists {
				if openEvidence(f) && securityEvidence(f) && !baselineOpen[progressKey(f)] {
					resurfaced[progressKey(f)] = true
					row.Resurfaced++
				}
				continue
			}
			key := progressKey(f)
			if !openEvidence(f) || !securityEvidence(f) || baselineOpen[key] {
				continue
			}
			if historical[key] {
				resurfaced[key] = true
				row.Resurfaced++
			} else {
				newKeys[key] = true
				row.New++
			}
		}
		for id, f := range baseByID {
			if seen[id] {
				continue
			}
			key := progressKey(f)
			if openEvidence(f) && securityEvidence(f) && !currentKeys[key] && !unverified[key] {
				resolved[key] = true
				row.Resolved++
			}
		}
		beforeOpen := map[string]bool{}
		for _, f := range before {
			if openEvidence(f) && securityEvidence(f) {
				beforeOpen[progressKey(f)] = true
			}
		}
		for _, f := range after {
			if beforeOpen[progressKey(f)] && !openEvidence(f) && securityEvidence(f) {
				suppressed[progressKey(f)] = true
				row.Suppressed++
			}
		}
		beforeLocations = append(beforeLocations, e.findings[row.BaselineScanID]...)
		afterLocations = append(afterLocations, e.findings[row.ScanID]...)
	}
	// Compatibility field: actual location-count change within the comparable
	// cohort, deduplicated across branches. The dashboard uses explicit changes.
	beforeLocations = slices.DeleteFunc(beforeLocations, func(f *fleetFinding) bool { return !openEvidence(f) })
	afterLocations = slices.DeleteFunc(afterLocations, func(f *fleetFinding) bool { return !openEvidence(f) })
	delta, previous := countBySeverity(dedupCurrentFindings(afterLocations)), countBySeverity(dedupCurrentFindings(beforeLocations))
	for sev, n := range previous {
		delta[sev] -= n
	}
	out.New, out.Resurfaced, out.Resolved, out.Suppressed = len(newKeys), len(resurfaced), len(resolved), len(suppressed)
	return out, delta
}

func (e *fleetEvidence) posture(now time.Time) *FleetPostureResult {
	out := &FleetPostureResult{RepoCount: len(e.repos), GeneratedAt: now, Priorities: []FleetPriority{}, Work: []FleetScanWork{}}
	current := e.currentOpen(now)
	out.OpenFindingsBySeverity = countBySeverity(current)
	out.Coverage = e.coverage(now)
	out.Changes, out.WeekOverWeekDelta = e.changes(now, out.Coverage)
	failing := map[string]bool{}
	for _, r := range out.Coverage.Rows {
		if r.GateFailing {
			failing[r.RepoID] = true
		}
	}
	out.GatesFailing = len(failing)
	names := map[string]string{}
	for _, r := range e.repos {
		names[r.ID] = r.Name
	}
	firstSeen := map[string]time.Time{}
	resurfacedAt := map[string]time.Time{}
	for _, s := range e.scans {
		for _, f := range e.findings[s.ID] {
			key := progressKey(f)
			at := scanObservedAt(s)
			if f.BaselineState == findingdiff.StateResurfaced && at.After(resurfacedAt[key]) {
				resurfacedAt[key] = at
			}
			if prev, ok := firstSeen[key]; !ok || at.Before(prev) {
				firstSeen[key] = at
			}
		}
	}
	out.Priorities = make([]FleetPriority, 0, out.OpenFindingsBySeverity["critical"]+out.OpenFindingsBySeverity["high"])
	for _, f := range current {
		if !securityEvidence(f) || severityRankName(f.Severity) < 4 {
			continue
		}
		state := "existing"
		if !firstSeen[progressKey(f)].Before(now.AddDate(0, 0, -7)) {
			state = "new"
		} else if !resurfacedAt[progressKey(f)].Before(now.AddDate(0, 0, -7)) {
			state = "resurfaced"
		}
		out.Priorities = append(out.Priorities, FleetPriority{FindingID: f.ID, RepoID: f.RepoID, Name: names[f.RepoID], Title: f.Title, Severity: f.Severity, Tool: f.ToolName, FilePath: f.FilePath, Line: f.LineStart, State: state, Confidence: f.Confidence})
	}
	sort.Slice(out.Priorities, func(i, j int) bool {
		a, b := out.Priorities[i], out.Priorities[j]
		if (a.State == "existing") != (b.State == "existing") {
			return a.State != "existing"
		}
		if a.Severity != b.Severity {
			return severityRankName(a.Severity) > severityRankName(b.Severity)
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.FindingID < b.FindingID
	})
	for _, s := range e.scans {
		if s.Status != models.ScanStatusPending && s.Status != models.ScanStatusRunning {
			continue
		}
		stalled := s.Status == models.ScanStatusRunning && s.LeaseExpiresAt != nil && s.LeaseExpiresAt.Before(now)
		out.Work = append(out.Work, FleetScanWork{ScanID: s.ID, RepoID: s.RepoID, Name: names[s.RepoID], Branch: s.Branch, Status: string(s.Status), Phase: s.Phase, Stalled: stalled})
	}
	return out
}

func (e *fleetEvidence) attention(now time.Time) []NeedsAttentionRow {
	p := e.posture(now)
	rows := map[string]NeedsAttentionRow{}
	for _, r := range p.Coverage.Rows {
		reason, detail, score := "", r.Detail, 0
		switch r.Status {
		case "never_scanned", "stale":
			reason, score = "stale", 30
		case "failed":
			reason, score = "scan_failed", 60
		case "incomplete":
			reason, score = "incomplete", 50
		}
		if r.GateFailing {
			reason, detail, score = "gate_failing", "Latest scan on this branch failed its gate.", 80
		}
		if score > rows[r.RepoID].Score {
			rows[r.RepoID] = NeedsAttentionRow{RepoID: r.RepoID, Name: r.Name, Reason: reason, Detail: detail, Score: score, ScanID: r.ScanID, Branch: r.Branch}
		}
	}
	for _, f := range p.Priorities {
		if f.State == "existing" {
			continue
		}
		score := severityRankName(f.Severity) * 20
		if score > rows[f.RepoID].Score {
			rows[f.RepoID] = NeedsAttentionRow{RepoID: f.RepoID, Name: f.Name, Reason: "new_high", Detail: fmt.Sprintf("%s %s: %s", f.State, f.Severity, f.Title), Score: score}
		}
	}
	out := make([]NeedsAttentionRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].RepoID < out[j].RepoID
	})
	return out
}

func fleetFindingTrends(ctx context.Context, database *sqlx.DB, userID string, fleetMode bool, collectionID string) ([]FleetTrendEntry, error) {
	e, err := loadFleetEvidence(ctx, database, userID, fleetMode, collectionID, "")
	if err != nil {
		return nil, err
	}
	out := []FleetTrendEntry{}
	if len(e.scans) == 0 {
		return out, nil
	}
	now := time.Now().UTC()
	start := scanObservedAt(e.scans[0]).Truncate(24 * time.Hour)
	if cutoff := now.Truncate(24*time.Hour).AddDate(0, 0, -89); start.Before(cutoff) {
		start = cutoff
	}
	for day := start; !day.After(now); day = day.AddDate(0, 0, 1) {
		at := day.AddDate(0, 0, 1).Add(-time.Nanosecond)
		counts := countBySeverity(e.currentOpen(at))
		total := 0
		for _, n := range counts {
			total += n
		}
		counts["total"] = total
		latest := map[string]models.Scan{}
		repos := map[string]bool{}
		for _, s := range e.scans {
			if !scanObservedAt(s).After(at) {
				latest[branchKey(s)] = s
				repos[s.RepoID] = true
			}
		}
		incomplete := 0
		for _, s := range latest {
			if !completeEvidence(s) {
				incomplete++
			}
		}
		out = append(out, FleetTrendEntry{Date: day.Format("2006-01-02"), Counts: counts, Repos: len(repos), IncompleteScans: incomplete})
	}
	return out, nil
}

func (s *SQLiteStore) FleetFindingTrends(ctx context.Context, userID string, fleetMode bool, collectionID string) ([]FleetTrendEntry, error) {
	return fleetFindingTrends(ctx, s.db, userID, fleetMode, collectionID)
}
func (s *PostgresStore) FleetFindingTrends(ctx context.Context, userID string, fleetMode bool, collectionID string) ([]FleetTrendEntry, error) {
	return fleetFindingTrends(ctx, s.db, userID, fleetMode, collectionID)
}
