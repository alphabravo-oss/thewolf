package db

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"runtime/trace"
	"slices"
	"sort"
	"strings"
	"time"

	findingdiff "github.com/alphabravocompany/thewolf/internal/finding/diff"
	"github.com/alphabravocompany/thewolf/internal/finding/gates"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/jmoiron/sqlx"
	"golang.org/x/sync/errgroup"
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
	// FirstSeenAt is the earliest scan observing this issue (Home age buckets).
	FirstSeenAt time.Time `json:"first_seen_at"`
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

// fleetEvidence holds scan metadata up front and finding rows per scan, on
// demand: current state needs only the latest complete scan of each branch and
// the attempts after it, never the whole history. Fixtures fill findings
// directly (src == nil); a failed load is sticky in err.
type fleetEvidence struct {
	repos       []fleetRepo
	scans       []models.Scan
	findings    map[string][]*fleetFinding
	failedGates map[string]bool
	src         *evidenceSource
	err         error
}

// evidenceSource is the scoped database behind lazy loads. root is the FROM
// clause of the root scans in scope (see loadFleetEvidence).
type evidenceSource struct {
	ctx  context.Context
	db   sqlx.ExtContext
	root string
	args []any
}

// Scoped scan metadata is cheap (one row per scan). Finding rows are not, so
// they are fetched per scan, a few at a time, and only the columns evidence
// reads. PostgreSQL returns created_at as epoch microseconds: decoding
// timestamps dominated the row cost.
const (
	fleetFindingColumns = `f.id, f.repo_id, f.fingerprint, f.stable_fingerprint, f.location_fingerprint, f.tool_name, f.category, f.severity, f.title, f.file_path, f.line_start, f.rule_id, f.status, f.suppressed, f.confidence`
	fleetLoadWorkers    = 8
)

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
	// Remediation scans are not backlog evidence: child scans carry
	// origin_scan_id, and older root scans of Wolf's own fix branches do not.
	root := ` FROM scans s JOIN repos r ON r.id = s.repo_id WHERE TRIM(COALESCE(s.origin_scan_id, '')) = '' AND COALESCE(s.branch, '') NOT LIKE 'wolf-fix/%'` + scope
	if err := sqlx.SelectContext(ctx, database, &e.scans, database.Rebind(`SELECT s.id, s.repo_id, s.branch, s.source_type, s.source_path, s.remote_node_id, s.profile, s.categories, s.include_paths, s.exclude_paths, s.status, s.tools_selected, s.tools_completed, s.tools_failed, s.scanner_release_id, s.release_manifest_digest, s.created_at, s.completed_at, s.phase, s.lease_expires_at, s.heartbeat_at`+root+` ORDER BY s.created_at, s.id`), args...); err != nil {
		return nil, err
	}
	var failed []string
	if err := sqlx.SelectContext(ctx, database, &failed, database.Rebind(`SELECT DISTINCT g.scan_id FROM quality_gate_results g WHERE g.status = 'fail' AND g.scan_id IN (SELECT s.id`+root+`)`), args...); err != nil {
		return nil, err
	}
	for _, id := range failed {
		e.failedGates[id] = true
	}
	e.src = &evidenceSource{ctx: ctx, db: database, root: root, args: args}
	return e, nil
}

// load reads one scan's finding evidence in finding-id order.
func (src *evidenceSource) load(scanID string) ([]*fleetFinding, error) {
	created, postgres := `f.created_at`, src.db.DriverName() == "postgres"
	if postgres {
		created = `(EXTRACT(EPOCH FROM f.created_at) * 1000000)::bigint`
	}
	rows, err := src.db.QueryContext(src.ctx, src.db.Rebind(`SELECT `+fleetFindingColumns+`, `+created+` FROM findings f WHERE f.scan_id = ? AND (f.cluster_id = '' OR f.cluster_id = f.id) ORDER BY f.id`), scanID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	// Repeated labels and paths dominate retained strings in large scans.
	labels := map[string]string{}
	intern := func(value string) string {
		if existing, ok := labels[value]; ok {
			return existing
		}
		labels[value] = value
		return value
	}
	var out []*fleetFinding
	for rows.Next() {
		f := &fleetFinding{ScanID: scanID}
		var category, severity, status string
		var at time.Time
		var micros int64
		created := any(&at)
		if postgres {
			created = &micros
		}
		if err := rows.Scan(&f.ID, &f.RepoID, &f.Fingerprint, &f.StableFingerprint, &f.LocationFingerprint, &f.ToolName, &category, &severity, &f.Title, &f.FilePath, &f.LineStart, &f.RuleID, &status, &f.Suppressed, &f.Confidence, created); err != nil {
			return nil, err
		}
		if postgres {
			at = time.UnixMicro(micros).UTC()
		}
		f.CreatedAt = at
		f.RepoID, f.ToolName, f.RuleID = intern(f.RepoID), intern(f.ToolName), intern(f.RuleID)
		f.Title, f.FilePath, f.Confidence = intern(f.Title), intern(f.FilePath), intern(f.Confidence)
		f.Category, f.Severity, f.Status = models.Category(intern(category)), models.Severity(intern(severity)), models.Status(intern(status))
		out = append(out, f)
	}
	return out, rows.Err()
}

// prefetch loads the named scans' findings, a few at a time on a pool (a
// transaction has one connection, so it loads serially). Callers that know
// their scan set up front use it; of() covers the stragglers.
func (e *fleetEvidence) prefetch(ids []string) {
	if e.src == nil || e.err != nil {
		return
	}
	var missing []string
	queued := map[string]bool{}
	for _, id := range ids {
		if _, ok := e.findings[id]; !ok && !queued[id] {
			queued[id] = true
			missing = append(missing, id)
		}
	}
	loaded := make([][]*fleetFinding, len(missing))
	var group errgroup.Group
	group.SetLimit(1)
	if _, ok := e.src.db.(*sqlx.DB); ok {
		group.SetLimit(fleetLoadWorkers)
	}
	for i, id := range missing {
		group.Go(func() (err error) { loaded[i], err = e.src.load(id); return })
	}
	if e.err = group.Wait(); e.err != nil {
		return
	}
	for i, id := range missing {
		e.findings[id] = loaded[i]
	}
}

func (e *fleetEvidence) of(scanID string) []*fleetFinding {
	e.prefetch([]string{scanID})
	return e.findings[scanID]
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

// contributing returns, per branch, the scans that make up its evidence at a
// moment: the latest complete scan and every later attempt, in creation order.
// Incomplete/cancelled attempts may add evidence, never erase the last complete
// observation; older scans are fully superseded and never need loading.
func (e *fleetEvidence) contributing(at time.Time) map[string][]models.Scan {
	chains := map[string][]models.Scan{}
	for _, s := range e.scans {
		if scanObservedAt(s).After(at) {
			continue
		}
		key := branchKey(s)
		if completeEvidence(s) {
			chains[key] = chains[key][:0]
		}
		chains[key] = append(chains[key], s)
	}
	return chains
}

func (e *fleetEvidence) currentEvidence(at time.Time) []*fleetFinding {
	return e.chainEvidence(e.contributing(at))
}

func (e *fleetEvidence) chainEvidence(chains map[string][]models.Scan) []*fleetFinding {
	var ids []string
	for _, chain := range chains {
		for _, s := range chain {
			ids = append(ids, s.ID)
		}
	}
	e.prefetch(ids)
	count := 0
	branches := make([]map[string]*fleetFinding, 0, len(chains))
	for _, chain := range chains {
		// A repeated finding's latest triage state wins.
		branch := map[string]*fleetFinding{}
		for _, s := range chain {
			for _, f := range e.findings[s.ID] {
				branch[findingIdent(f)] = f
			}
		}
		count += len(branch)
		branches = append(branches, branch)
	}
	findings := make([]*fleetFinding, 0, count)
	for _, branch := range branches {
		for _, f := range branch {
			findings = append(findings, f)
		}
	}
	return findings
}

// history reports, for the progress key of each given finding, the earliest
// scan observing the issue and the latest scan that flagged it resurfaced,
// across every root scan in scope. Only matching rows are read, one per scan
// and issue, so history is never loaded wholesale.
func (e *fleetEvidence) history(findings []*fleetFinding) (first, resurfaced map[string]time.Time) {
	first, resurfaced = map[string]time.Time{}, map[string]time.Time{}
	if len(findings) == 0 {
		return
	}
	observed := make(map[string]time.Time, len(e.scans))
	for _, s := range e.scans {
		observed[s.ID] = scanObservedAt(s)
	}
	note := func(scanID string, f *fleetFinding, flagged bool) {
		key, at := progressKey(f), observed[scanID]
		if prev, ok := first[key]; !ok || at.Before(prev) {
			first[key] = at
		}
		if flagged && at.After(resurfaced[key]) {
			resurfaced[key] = at
		}
	}
	if e.src == nil {
		for _, s := range e.scans {
			for _, f := range e.findings[s.ID] {
				note(s.ID, f, f.BaselineState == findingdiff.StateResurfaced)
			}
		}
		return
	}
	// A key is the stable fingerprint, else the fingerprint, else a location
	// identity that cannot be matched in SQL: those need their own scan.
	var parts []string
	byLocation := false
	for _, f := range findings {
		if part := cmp.Or(f.StableFingerprint, f.Fingerprint); part != "" {
			parts = append(parts, part)
		} else {
			byLocation = true
		}
	}
	slices.Sort(parts)
	parts = slices.Compact(parts)
	src := e.src
	visible := `f.scan_id IN (SELECT s.id` + src.root + `) AND (f.cluster_id = '' OR f.cluster_id = f.id)`
	for chunk := range slices.Chunk(parts, 2000) {
		query, args, err := sqlx.In(`SELECT f.scan_id, f.repo_id, f.stable_fingerprint, f.fingerprint, MAX(CASE WHEN f.baseline_state = ? THEN 1 ELSE 0 END) FROM findings f WHERE `+visible+` AND (f.stable_fingerprint IN (?) OR (f.stable_fingerprint = '' AND f.fingerprint IN (?))) GROUP BY f.scan_id, f.repo_id, f.stable_fingerprint, f.fingerprint`,
			append(append([]any{findingdiff.StateResurfaced}, src.args...), chunk, chunk)...)
		if err == nil {
			err = e.scanHistory(src.db.Rebind(query), args, func(rows *sql.Rows) (string, *fleetFinding, bool, error) {
				var scanID string
				var flagged int
				f := &fleetFinding{}
				err := rows.Scan(&scanID, &f.RepoID, &f.StableFingerprint, &f.Fingerprint, &flagged)
				return scanID, f, flagged > 0, err
			}, note)
		}
		if err != nil {
			e.err = err
			return
		}
	}
	if byLocation {
		err := e.scanHistory(src.db.Rebind(`SELECT f.scan_id, f.id, f.repo_id, f.location_fingerprint, f.tool_name, f.rule_id, f.file_path, f.line_start, f.baseline_state FROM findings f WHERE `+visible+` AND f.stable_fingerprint = '' AND f.fingerprint = ''`), src.args, func(rows *sql.Rows) (string, *fleetFinding, bool, error) {
			var scanID, state string
			f := &fleetFinding{}
			err := rows.Scan(&scanID, &f.ID, &f.RepoID, &f.LocationFingerprint, &f.ToolName, &f.RuleID, &f.FilePath, &f.LineStart, &state)
			return scanID, f, state == findingdiff.StateResurfaced, err
		}, note)
		if err != nil {
			e.err = err
		}
	}
	return
}

func (e *fleetEvidence) scanHistory(query string, args []any, read func(*sql.Rows) (string, *fleetFinding, bool, error), note func(string, *fleetFinding, bool)) error {
	rows, err := e.src.db.QueryContext(e.src.ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		scanID, f, flagged, err := read(rows)
		if err != nil {
			return err
		}
		note(scanID, f, flagged)
	}
	return rows.Err()
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
	// Whether an issue first appeared in this window or came back after being
	// seen before it is read from history without loading that history: only
	// issues absent from their baseline can be either.
	type comparison struct {
		before, after []*fleetFinding
		baseByID      map[string]*fleetFinding
	}
	compared := map[int]comparison{}
	var candidates []*fleetFinding
	for i := range out.Rows {
		row := &out.Rows[i]
		if row.Reason != "" {
			continue
		}
		before, after := distinctProgress(e.of(row.BaselineScanID)), distinctProgress(e.of(row.ScanID))
		// Include suppressed observations in the diff: hiding an issue cannot
		// masquerade as its absence in a verification scan.
		baseByID := make(map[string]*fleetFinding, len(before))
		for _, f := range before {
			key := f.diffKey()
			if _, exists := baseByID[key]; key != "" && !exists {
				baseByID[key] = f
			}
		}
		compared[i] = comparison{before, after, baseByID}
		for _, f := range after {
			if _, exists := baseByID[f.diffKey()]; !exists && openEvidence(f) && securityEvidence(f) && !baselineOpen[progressKey(f)] {
				candidates = append(candidates, f)
			}
		}
	}
	firstSeen, _ := e.history(candidates)
	for i := range out.Rows {
		row := &out.Rows[i]
		if row.Reason != "" {
			continue
		}
		before, after, baseByID := compared[i].before, compared[i].after, compared[i].baseByID
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
			if first, ok := firstSeen[key]; ok && !first.After(cutoff) {
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
		beforeLocations = append(beforeLocations, e.of(row.BaselineScanID)...)
		afterLocations = append(afterLocations, e.of(row.ScanID)...)
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
	var high []*fleetFinding
	for _, f := range current {
		if securityEvidence(f) && severityRankName(f.Severity) >= 4 {
			high = append(high, f)
		}
	}
	firstSeen, resurfacedAt := e.history(high)
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
		out.Priorities = append(out.Priorities, FleetPriority{FindingID: f.ID, RepoID: f.RepoID, Name: names[f.RepoID], Title: f.Title, Severity: f.Severity, Tool: f.ToolName, FilePath: f.FilePath, Line: f.LineStart, State: state, Confidence: f.Confidence, FirstSeenAt: firstSeen[progressKey(f)]})
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
	// The evidence only changes when a scan finishes, so identical scan chains
	// share one count and the days before the window never load their scans.
	type day struct {
		date   time.Time
		chains map[string][]models.Scan
		id     string
	}
	var days []day
	var ids []string
	for date := start; !date.After(now); date = date.AddDate(0, 0, 1) {
		chains := e.contributing(date.AddDate(0, 0, 1).Add(-time.Nanosecond))
		keys := slices.Sorted(maps.Keys(chains))
		var id strings.Builder
		for _, key := range keys {
			id.WriteString(key + "\x00")
			for _, s := range chains[key] {
				id.WriteString(s.ID + "\x00")
				ids = append(ids, s.ID)
			}
		}
		days = append(days, day{date, chains, id.String()})
	}
	e.prefetch(ids)
	counted := map[string]map[string]int{}
	for _, d := range days {
		at := d.date.AddDate(0, 0, 1).Add(-time.Nanosecond)
		if counted[d.id] == nil {
			current := e.chainEvidence(d.chains)
			current = slices.DeleteFunc(current, func(f *fleetFinding) bool { return !openEvidence(f) })
			counted[d.id] = countBySeverity(dedupCurrentFindings(current))
		}
		counts := maps.Clone(counted[d.id])
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
		out = append(out, FleetTrendEntry{Date: d.date.Format("2006-01-02"), Counts: counts, Repos: len(repos), IncompleteScans: incomplete})
	}
	if e.err != nil {
		return nil, e.err
	}
	return out, nil
}

func (s *SQLiteStore) FleetFindingTrends(ctx context.Context, userID string, fleetMode bool, collectionID string) ([]FleetTrendEntry, error) {
	return fleetFindingTrends(ctx, s.db, userID, fleetMode, collectionID)
}
func (s *PostgresStore) FleetFindingTrends(ctx context.Context, userID string, fleetMode bool, collectionID string) ([]FleetTrendEntry, error) {
	return fleetFindingTrends(ctx, s.db, userID, fleetMode, collectionID)
}
