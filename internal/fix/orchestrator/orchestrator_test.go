package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/fix/hygiene"
	"github.com/alphabravocompany/thewolf/internal/fix/verify"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// --- stubs (no real agent CLIs, docker, or network) ---

// stubStore implements the orchestrator's Store interface in memory and records
// every FixAttempt + job update so tests can assert the audit trail.
type stubStore struct {
	repo         *models.Repo
	findings     map[string]*models.Finding
	attempts     []models.FixAttempt
	jobs         []models.FixJob // every UpdateFixJob snapshot, in order
	suppressions []models.FindingSuppression
}

func (s *stubStore) GetRepoByID(_ context.Context, _ string) (*models.Repo, error) {
	return s.repo, nil
}
func (s *stubStore) GetFixJobByID(_ context.Context, id string) (*models.FixJob, error) {
	for i := len(s.jobs) - 1; i >= 0; i-- {
		if s.jobs[i].ID == id {
			j := s.jobs[i]
			return &j, nil
		}
	}
	return nil, nil
}
func (s *stubStore) GetFindingByID(_ context.Context, id string) (*models.Finding, error) {
	return s.findings[id], nil
}
func (s *stubStore) UpdateFixJob(_ context.Context, job *models.FixJob) error {
	s.jobs = append(s.jobs, *job)
	return nil
}
func (s *stubStore) CreateFixAttempt(_ context.Context, att *models.FixAttempt) error {
	s.attempts = append(s.attempts, *att)
	return nil
}
func (s *stubStore) ListFindingsByScan(context.Context, string) ([]models.Finding, error) {
	return nil, nil
}
func (s *stubStore) GetRemediationByID(context.Context, string) (*models.Remediation, error) {
	return nil, nil
}
func (s *stubStore) UpdateRemediation(context.Context, *models.Remediation) error { return nil }
func (s *stubStore) CreateFindingSuppression(_ context.Context, sup *models.FindingSuppression) error {
	if s == nil || sup == nil {
		return nil
	}
	s.suppressions = append(s.suppressions, *sup)
	return nil
}

// stubWorkspace is a fake working tree. It tracks rollbacks and pushes; the
// orchestrator must NEVER push (v1 is branch-only), so pushes is asserted zero.
type stubWorkspace struct {
	branch       string
	path         string
	changed      []string
	rolledBack   []string
	cleanupCalls int
	pushCalls    int
	pushErr      error
	commitCalls  int
}

func (w *stubWorkspace) Path() string {
	if w.path != "" {
		return w.path
	}
	return "/ws"
}
func (w *stubWorkspace) ChangedFiles(context.Context) ([]string, error) {
	return w.changed, nil
}
func (w *stubWorkspace) Rollback(_ context.Context, file string) error {
	w.rolledBack = append(w.rolledBack, file)
	return nil
}
func (w *stubWorkspace) Branch() string { return w.branch }
func (w *stubWorkspace) Diff(context.Context) (string, error) {
	return "diff --git a/main.go b/main.go\n@@ fix @@\n", nil
}
func (w *stubWorkspace) Cleanup(context.Context) error { w.cleanupCalls++; return nil }
func (w *stubWorkspace) Commit(context.Context, string) error {
	w.commitCalls++
	return nil
}
func (w *stubWorkspace) Push(context.Context) (string, error) {
	w.pushCalls++
	if w.pushErr != nil {
		return "", w.pushErr
	}
	return "abc123", nil
}

type stubPreparer struct {
	ws  *stubWorkspace
	err error
}

func (p *stubPreparer) Prepare(_ context.Context, _ *models.Repo, branch string) (Workspace, error) {
	if p.err != nil {
		return nil, p.err
	}
	p.ws.branch = branch
	return p.ws, nil
}
func (p *stubPreparer) Open(context.Context, string, *models.Repo) (Workspace, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.ws, nil
}

// fixEngine is a stub SubprocessEngine whose Fix returns a canned in-place edit
// result. name distinguishes tiers in the chain.
type fixEngine struct {
	name string
	fail bool // when true, Fix reports no successful fix
}

func (e *fixEngine) Name() string    { return e.name }
func (e *fixEngine) Available() bool { return true }
func (e *fixEngine) Fix(_ context.Context, req engine.FixRequest) (*engine.FixResult, error) {
	if e.fail {
		return &engine.FixResult{Success: false, Error: "engine could not fix"}, nil
	}
	files := []string{"main.go"}
	if req.Finding.FilePath != "" {
		files = []string{req.Finding.FilePath}
	}
	return &engine.FixResult{Success: true, EditsInPlace: true, FilesChanged: files}, nil
}

// stubChain walks a fixed list of engines via Current/Next.
type stubChain struct {
	tiers []engine.SubprocessEngine
	idx   int
}

func (c *stubChain) Current() engine.SubprocessEngine {
	if c.idx >= len(c.tiers) {
		return nil
	}
	return c.tiers[c.idx]
}
func (c *stubChain) Next() engine.SubprocessEngine {
	c.idx++
	return c.Current()
}

// chainSelector returns a fresh chain per finding (so each finding starts at the
// top tier), built from the configured engine factory.
type chainSelector struct {
	build func() EngineChain
}

func (s *chainSelector) Select(context.Context, *models.FixJob) (EngineChain, error) {
	return s.build(), nil
}

// stubVerifier judges by a per-finding verdict map keyed by finding ID. A
// missing key fails the gate (the default for an escalating finding). Each
// finding's verdict can be a list consumed per attempt so the same finding fails
// then... (we keep it simple: a single verdict per finding ID).
type stubVerifier struct {
	pass map[string]bool
}

func (v *stubVerifier) Verify(_ context.Context, _ verify.Workspace, f models.Finding) (*verify.VerifyResult, error) {
	ok := v.pass[f.ID]
	return &verify.VerifyResult{
		Passed:         ok,
		Built:          true,
		FindingCleared: ok,
		ChangedFiles:   []string{"main.go"},
	}, nil
}
func (v *stubVerifier) VerifyBatch(ctx context.Context, ws verify.Workspace, findings []models.Finding) (map[string]*verify.VerifyResult, error) {
	out := make(map[string]*verify.VerifyResult, len(findings))
	for _, f := range findings {
		vr, err := v.Verify(ctx, ws, f)
		if err != nil {
			return out, err
		}
		out[f.ID] = vr
	}
	return out, nil
}

// stubDiffs captures the persisted diff in memory (no real artifact store).
type stubDiffs struct {
	jobID string
	diff  string
	id    string
}

func (d *stubDiffs) SaveDiff(_ context.Context, jobID, diff string) (string, error) {
	d.jobID = jobID
	d.diff = diff
	d.id = "artifact-1"
	return d.id, nil
}

func twoFindingJob() *models.FixJob {
	return &models.FixJob{
		ID:            "job-1",
		RepoID:        "repo-1",
		Type:          "fix",
		Mode:          models.FixModeDryRun,
		FindingIDList: []string{"keep-me", "escalate-me"},
		MaxAttempts:   2,
	}
}

func baseStore() *stubStore {
	return &stubStore{
		repo: &models.Repo{ID: "repo-1", SourceType: models.SourceTypeLocal, SourcePath: "/repo"},
		findings: map[string]*models.Finding{
			"keep-me":     {ID: "keep-me", FilePath: "main.go", Severity: models.SeverityHigh, ToolName: "gosec", RuleID: "G1"},
			"escalate-me": {ID: "escalate-me", FilePath: "main.go", Severity: models.SeverityHigh, ToolName: "gosec", RuleID: "G2"},
		},
	}
}

// TestRun_KeepAndEscalateThenUnfixable is the headline Phase 5 test: a 2-finding
// job where one fix is kept on the first attempt and the other escalates through
// the engine chain and ends unfixable. It asserts the attempts are recorded, the
// branch is assembled with a diff artifact, the job succeeds, and NO push ever
// happens (v1 is branch-only).
func TestRun_KeepAndEscalateThenUnfixable(t *testing.T) {
	store := baseStore()
	ws := &stubWorkspace{changed: []string{"main.go"}}
	diffs := &stubDiffs{}

	deps := Deps{
		Store:       store,
		Writability: passWritability{},
		Workspaces:  &stubPreparer{ws: ws},
		Engines: &chainSelector{build: func() EngineChain {
			// Two tiers so an escalating finding gets a second engine attempt.
			return &stubChain{tiers: []engine.SubprocessEngine{
				&fixEngine{name: "claude-code"},
				&fixEngine{name: "api"},
			}}
		}},
		// keep-me passes the gate; escalate-me never does, forcing escalation
		// then an unfixable verdict.
		Verifier: &stubVerifier{pass: map[string]bool{"keep-me": true}},
		Diffs:    diffs,
	}

	res, err := Run(context.Background(), twoFindingJob(), deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// --- Branch assembled + diff artifact persisted. ---
	if res.Branch == "" {
		t.Fatal("expected a result branch")
	}
	if res.DiffArtifactID != "artifact-1" {
		t.Fatalf("expected diff artifact id, got %q", res.DiffArtifactID)
	}
	if diffs.diff == "" {
		t.Fatal("expected the assembled diff to be persisted")
	}

	// --- Summary tallies one kept + one unfixable. ---
	if res.Summary.Kept != 1 {
		t.Fatalf("expected 1 kept, got %d", res.Summary.Kept)
	}
	if res.Summary.Unfixable != 1 {
		t.Fatalf("expected 1 unfixable, got %d", res.Summary.Unfixable)
	}
	if res.Summary.Considered != 2 {
		t.Fatalf("expected 2 considered, got %d", res.Summary.Considered)
	}

	// --- Attempts recorded: keep-me once (kept); escalate-me twice
	// (rolled_back across two tiers), the last marked unfixable. ---
	var keepAttempts, escalateAttempts []models.FixAttempt
	for _, a := range store.attempts {
		switch a.FindingID {
		case "keep-me":
			keepAttempts = append(keepAttempts, a)
		case "escalate-me":
			escalateAttempts = append(escalateAttempts, a)
		}
	}
	if len(keepAttempts) == 0 {
		t.Fatal("expected at least one recorded attempt for keep-me")
	}
	if got := keepAttempts[len(keepAttempts)-1].Outcome; got != models.FixOutcomeKept {
		t.Fatalf("keep-me final outcome = %q, want kept", got)
	}
	if len(escalateAttempts) < 2 {
		t.Fatalf("expected escalate-me to be attempted on 2 tiers, got %d attempts", len(escalateAttempts))
	}
	if got := escalateAttempts[len(escalateAttempts)-1].Outcome; got != models.FixOutcomeUnfixable {
		t.Fatalf("escalate-me final outcome = %q, want unfixable", got)
	}
	// The two escalate attempts must have used different engine tiers.
	if escalateAttempts[0].EngineUsed == escalateAttempts[1].EngineUsed {
		t.Fatalf("expected escalation across tiers, both used %q", escalateAttempts[0].EngineUsed)
	}

	// --- Job ends succeeded with the branch + artifact recorded. ---
	final := store.jobs[len(store.jobs)-1]
	if final.Status != models.FixJobSucceeded {
		t.Fatalf("job status = %q, want succeeded", final.Status)
	}
	if final.ResultBranch != res.Branch || final.DiffArtifactID != "artifact-1" {
		t.Fatalf("job not stamped with branch/artifact: %+v", final)
	}
	if final.Summary == "" {
		t.Fatal("expected job summary JSON to be persisted")
	}

	// dry_run on a local repo does not push.
	if ws.pushCalls != 0 {
		t.Fatalf("expected zero pushes, got %d", ws.pushCalls)
	}
	if ws.commitCalls == 0 {
		t.Fatal("expected kept findings to be committed")
	}
	if ws.cleanupCalls == 0 {
		t.Fatal("expected the workspace to be cleaned up")
	}
}

// TestRun_PreflightNotWritable_FailsFast asserts a non-writable repo aborts the
// job before any workspace/engine work.
func TestRun_PreflightNotWritable_FailsFast(t *testing.T) {
	store := baseStore()
	prep := &stubPreparer{ws: &stubWorkspace{}}
	deps := Deps{
		Store:       store,
		Writability: failWritability{reason: "github token lacks push permission"},
		Workspaces:  prep,
		Engines:     &chainSelector{build: func() EngineChain { return &stubChain{} }},
		Verifier:    &stubVerifier{},
	}

	_, err := Run(context.Background(), twoFindingJob(), deps)
	if err == nil {
		t.Fatal("expected Run to fail fast on a non-writable repo")
	}
	if prep.ws.cleanupCalls != 0 {
		t.Fatal("workspace should never be prepared when preflight fails")
	}
	final := store.jobs[len(store.jobs)-1]
	if final.Status != models.FixJobFailed {
		t.Fatalf("job status = %q, want failed", final.Status)
	}
}

// TestRun_SeverityFloorAndTriageFilter skips findings below the floor and those
// triaged away, recording no attempts for them.
func TestRun_SeverityFloorAndTriageFilter(t *testing.T) {
	store := &stubStore{
		repo: &models.Repo{ID: "repo-1", SourceType: models.SourceTypeLocal, SourcePath: "/repo"},
		findings: map[string]*models.Finding{
			"low":     {ID: "low", FilePath: "a.go", Severity: models.SeverityLow},
			"triaged": {ID: "triaged", FilePath: "b.go", Severity: models.SeverityCritical, Status: models.StatusFalsePositive},
			"high":    {ID: "high", FilePath: "c.go", Severity: models.SeverityHigh, ToolName: "gosec", RuleID: "G1"},
		},
	}
	job := &models.FixJob{
		ID:            "job-2",
		RepoID:        "repo-1",
		Mode:          models.FixModeDryRun,
		SeverityFloor: string(models.SeverityHigh),
		FindingIDList: []string{"low", "triaged", "high"},
		MaxAttempts:   1,
	}
	ws := &stubWorkspace{changed: []string{"c.go"}}
	deps := Deps{
		Store:       store,
		Writability: passWritability{},
		Workspaces:  &stubPreparer{ws: ws},
		Engines: &chainSelector{build: func() EngineChain {
			return &stubChain{tiers: []engine.SubprocessEngine{&fixEngine{name: "claude-code"}}}
		}},
		Verifier: &stubVerifier{pass: map[string]bool{"high": true}},
		Diffs:    &stubDiffs{},
	}

	res, err := Run(context.Background(), job, deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.Skipped != 2 {
		t.Fatalf("expected 2 skipped (low + triaged), got %d", res.Summary.Skipped)
	}
	if res.Summary.Considered != 1 || res.Summary.Kept != 1 {
		t.Fatalf("expected only the high finding fixed: %+v", res.Summary)
	}
	for _, a := range store.attempts {
		if a.FindingID == "low" || a.FindingID == "triaged" {
			t.Fatalf("filtered finding %q should have no recorded attempt", a.FindingID)
		}
	}
}

func TestGroupFindingsByToolRanksCriticalMass(t *testing.T) {
	in := []models.Finding{
		{ID: "b1", ToolName: "bearer", Severity: models.SeverityLow},
		{ID: "c1", ToolName: "checkov", Severity: models.SeverityCritical},
		{ID: "c2", ToolName: "checkov", Severity: models.SeverityHigh},
		{ID: "b2", ToolName: "bearer", Severity: models.SeverityCritical},
		{ID: "b3", ToolName: "bearer", Severity: models.SeverityCritical},
		{ID: "s1", ToolName: "semgrep", Severity: models.SeverityHigh},
	}
	got := groupFindingsByTool(in)
	if len(got) != 3 {
		t.Fatalf("groups = %d", len(got))
	}
	if got[0].Tool != "bearer" || got[0].Critical != 2 || len(got[0].Findings) != 3 {
		t.Fatalf("first group = %+v", got[0])
	}
	if got[1].Tool != "checkov" || got[1].Critical != 1 {
		t.Fatalf("second group = %+v", got[1])
	}
	if got[2].Tool != "semgrep" {
		t.Fatalf("third group = %s", got[2].Tool)
	}
}

func TestLocationKeyIgnoresTool(t *testing.T) {
	a := models.Finding{ToolName: "bearer", FilePath: "./Foo/a.go", LineStart: 10}
	b := models.Finding{ToolName: "semgrep", FilePath: "foo/a.go", LineStart: 10}
	if locationKey(a) == "" || locationKey(a) != locationKey(b) {
		t.Fatalf("same line should match: %q %q", locationKey(a), locationKey(b))
	}
	if locationKey(models.Finding{FilePath: "a.go", LineStart: 0}) != "" {
		t.Fatal("line 0 is not a location")
	}
}

func TestSplitAlreadyFixed(t *testing.T) {
	kept := models.Finding{ID: "b", ToolName: "bearer", FilePath: "a.go", LineStart: 10}
	locs := map[string]bool{}
	rememberKeptLocations(locs, []models.Finding{kept}, map[string]string{"b": models.FixOutcomeKept})
	work, overlap := splitAlreadyFixed([]models.Finding{
		{ID: "same", ToolName: "semgrep", FilePath: "a.go", LineStart: 10},
		{ID: "other", ToolName: "semgrep", FilePath: "a.go", LineStart: 20},
		{ID: "noline", ToolName: "semgrep", FilePath: "a.go"},
	}, locs)
	if len(overlap) != 1 || overlap[0].ID != "same" {
		t.Fatalf("overlap = %+v", overlap)
	}
	if len(work) != 2 {
		t.Fatalf("work = %+v", work)
	}
}

func TestRun_SkipsLaterToolAtSameLocation(t *testing.T) {
	store := &stubStore{
		repo: &models.Repo{ID: "repo-1", SourceType: models.SourceTypeLocal, SourcePath: "/repo"},
		findings: map[string]*models.Finding{
			"b": {ID: "b", ToolName: "bearer", FilePath: "a.go", LineStart: 10, Severity: models.SeverityCritical, RuleID: "cmd"},
			"same": {
				ID: "same", ToolName: "semgrep", FilePath: "a.go", LineStart: 10,
				Severity: models.SeverityHigh, RuleID: "other-rule",
			},
			"other": {
				ID: "other", ToolName: "semgrep", FilePath: "a.go", LineStart: 20,
				Severity: models.SeverityHigh, RuleID: "x",
			},
		},
	}
	var ids []string
	deps := Deps{
		Store:       store,
		Writability: passWritability{},
		Workspaces:  &stubPreparer{ws: &stubWorkspace{changed: []string{"a.go"}}},
		Engines: &chainSelector{build: func() EngineChain {
			return &stubChain{tiers: []engine.SubprocessEngine{&recordingEngine{ids: &ids, fixAll: true}}}
		}},
		Verifier: &stubVerifier{pass: map[string]bool{"b": true, "other": true}},
		Diffs:    &stubDiffs{},
	}
	job := &models.FixJob{
		ID: "job-overlap", RepoID: "repo-1", Mode: models.FixModeDryRun,
		FindingIDList: []string{"b", "same", "other"}, MaxAttempts: 1, MaxLoops: 1,
	}
	res, err := Run(context.Background(), job, deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, id := range ids {
		if id == "same" {
			t.Fatalf("semgrep finding on the already-kept line was sent to the agent: %v", ids)
		}
	}
	got := strings.Join(ids, ",")
	if !strings.Contains(got, "b") || !strings.Contains(got, "other") {
		t.Fatalf("engine ids = %v", ids)
	}
	if res.Summary.Kept < 1 {
		t.Fatalf("summary = %+v", res.Summary)
	}
}

func TestGroupFindingsByToolLintBeforeCode(t *testing.T) {
	in := []models.Finding{
		{ID: "b1", ToolName: "bearer", Severity: models.SeverityCritical},
		{ID: "y1", ToolName: "yamllint", Severity: models.SeverityMedium},
		{ID: "y2", ToolName: "yamllint", Severity: models.SeverityLow},
		{ID: "t1", ToolName: "trivy", Severity: models.SeverityHigh},
	}
	got := groupFindingsByTool(in)
	if len(got) != 3 {
		t.Fatalf("groups = %d", len(got))
	}
	if got[0].Tool != "yamllint" || got[1].Tool != "trivy" || got[2].Tool != "bearer" {
		t.Fatalf("order = %s, %s, %s", got[0].Tool, got[1].Tool, got[2].Tool)
	}
}

func TestSortFindingsHighestRiskFirst(t *testing.T) {
	in := []models.Finding{
		{ID: "med", Severity: models.SeverityMedium, CompositeScore: 90},
		{ID: "crit-lowscore", Severity: models.SeverityCritical, CompositeScore: 10},
		{ID: "crit-highscore", Severity: models.SeverityCritical, CompositeScore: 80, Confidence: "high"},
	}
	sortFindings(in)
	if in[0].ID != "crit-highscore" || in[1].ID != "crit-lowscore" || in[2].ID != "med" {
		t.Fatalf("order = %s, %s, %s", in[0].ID, in[1].ID, in[2].ID)
	}
}

func TestRun_SkipsSuppressed(t *testing.T) {
	store := &stubStore{
		repo: &models.Repo{ID: "repo-1", SourceType: models.SourceTypeLocal, SourcePath: "/repo"},
		findings: map[string]*models.Finding{
			"sup":  {ID: "sup", FilePath: "vendor.go", Severity: models.SeverityCritical, Suppressed: true, SuppressedReason: "default:vendor"},
			"high": {ID: "high", FilePath: "c.go", Severity: models.SeverityHigh, ToolName: "gosec", RuleID: "G1"},
		},
	}
	job := &models.FixJob{
		ID: "job-sup", RepoID: "repo-1", Mode: models.FixModeDryRun,
		FindingIDList: []string{"sup", "high"}, MaxAttempts: 1,
	}
	deps := Deps{
		Store: store, Writability: passWritability{},
		Workspaces: &stubPreparer{ws: &stubWorkspace{changed: []string{"c.go"}}},
		Engines: &chainSelector{build: func() EngineChain {
			return &stubChain{tiers: []engine.SubprocessEngine{&fixEngine{name: "claude-code"}}}
		}},
		Verifier: &stubVerifier{pass: map[string]bool{"high": true}},
		Diffs:    &stubDiffs{},
	}
	res, err := Run(context.Background(), job, deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.Skipped != 1 || res.Summary.Kept != 1 {
		t.Fatalf("summary = %+v", res.Summary)
	}
}

// TestRun_APIEngineDiffApplied asserts an API engine's returned diff (EditsInPlace
// false) is git-applied by the orchestrator before verification, never trusting
// the engine to have touched the filesystem.
func TestRun_APIEngineDiffApplied(t *testing.T) {
	store := baseStore()
	store.findings = map[string]*models.Finding{
		"api-fix": {ID: "api-fix", FilePath: "main.go", Severity: models.SeverityHigh, ToolName: "gosec", RuleID: "G1"},
	}
	job := &models.FixJob{
		ID: "job-3", RepoID: "repo-1", Mode: models.FixModeDryRun,
		FindingIDList: []string{"api-fix"}, MaxAttempts: 1,
	}
	applier := &recordingApplier{}
	deps := Deps{
		Store:       store,
		Writability: passWritability{},
		Workspaces:  &stubPreparer{ws: &stubWorkspace{changed: []string{"main.go"}}},
		Engines: &chainSelector{build: func() EngineChain {
			return &stubChain{tiers: []engine.SubprocessEngine{&apiDiffEngine{}}}
		}},
		Verifier: &stubVerifier{pass: map[string]bool{"api-fix": true}},
		GitApply: applier,
		Diffs:    &stubDiffs{},
	}

	res, err := Run(context.Background(), job, deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if applier.calls != 1 {
		t.Fatalf("expected the API diff to be git-applied once, got %d", applier.calls)
	}
	if res.Summary.Kept != 1 {
		t.Fatalf("expected the API fix to be kept, got %+v", res.Summary)
	}
	last := store.attempts[len(store.attempts)-1]
	if last.EngineUsed != "api" {
		t.Fatalf("engine_used = %q, want api", last.EngineUsed)
	}
}

// --- small stub helpers ---

type passWritability struct{}

func (passWritability) Check(context.Context, *models.Repo) (bool, string) {
	return true, "writable"
}

type failWritability struct{ reason string }

func (f failWritability) Check(context.Context, *models.Repo) (bool, string) {
	return false, f.reason
}

// apiDiffEngine returns a diff without editing files (EditsInPlace=false).
type apiDiffEngine struct{}

func (apiDiffEngine) Name() string    { return "api" }
func (apiDiffEngine) Available() bool { return true }
func (apiDiffEngine) Fix(context.Context, engine.FixRequest) (*engine.FixResult, error) {
	return &engine.FixResult{
		Success:      true,
		EditsInPlace: false,
		Diff:         "diff --git a/main.go b/main.go\n@@ @@\n-bad\n+good\n",
		FilesChanged: []string{"main.go"},
	}, nil
}

type recordingApplier struct {
	calls int
	err   error
}

func (r *recordingApplier) Apply(context.Context, string, string) error {
	r.calls++
	return r.err
}

func TestRun_HITLPausesBetweenLoops(t *testing.T) {
	store := baseStore()
	store.findings["leftover"] = &models.Finding{
		ID: "leftover", FilePath: "other.go", Severity: models.SeverityHigh,
		ToolName: "gosec", RuleID: "G3",
	}
	ws := &stubWorkspace{changed: []string{"main.go"}}
	job := twoFindingJob()
	job.FindingIDList = []string{"keep-me", "leftover"}
	job.HumanInTheLoop = true
	job.MaxLoops = 2
	job.MaxAttempts = 1
	deps := Deps{
		Store:       store,
		Writability: passWritability{},
		Workspaces:  &stubPreparer{ws: ws},
		Engines: &chainSelector{build: func() EngineChain {
			return &stubChain{tiers: []engine.SubprocessEngine{&fixEngine{name: "claude-code"}}}
		}},
		Verifier: &stubVerifier{pass: map[string]bool{"keep-me": true}},
		Diffs:    &stubDiffs{},
		Rescan: remainingRescanner{keys: map[string]bool{
			"gosec|G3|other.go": true,
		}},
	}
	res, err := Run(context.Background(), job, deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.PauseStatus != models.FixJobAwaitingReview {
		t.Fatalf("pause = %q, want awaiting_review", res.PauseStatus)
	}
	if ws.cleanupCalls != 0 {
		t.Fatal("paused job must keep the workspace")
	}
	final := store.jobs[len(store.jobs)-1]
	if final.Status != models.FixJobAwaitingReview {
		t.Fatalf("job status = %q", final.Status)
	}
}

func TestRun_UntouchedFindingsStayOpen(t *testing.T) {
	store := baseStore()
	store.findings["other"] = &models.Finding{
		ID: "other", FilePath: "other.go", Severity: models.SeverityHigh,
		ToolName: "gosec", RuleID: "Gx",
	}
	ws := &stubWorkspace{changed: []string{"main.go"}}
	job := twoFindingJob()
	job.FindingIDList = []string{"keep-me", "other"}
	job.MaxAttempts = 1
	var verified []string
	deps := Deps{
		Store:       store,
		Writability: passWritability{},
		Workspaces:  &stubPreparer{ws: ws},
		Engines: &chainSelector{build: func() EngineChain {
			return &stubChain{tiers: []engine.SubprocessEngine{&stickyFileEngine{name: "claude-code", file: "main.go"}}}
		}},
		Verifier: &verifyRecorder{pass: map[string]bool{"keep-me": true}, seen: &verified},
		Diffs:    &stubDiffs{},
	}
	res, err := Run(context.Background(), job, deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.Kept != 1 {
		t.Fatalf("kept=%d want 1", res.Summary.Kept)
	}
	for _, a := range store.attempts {
		if a.FindingID == "other" {
			t.Fatalf("untouched finding should not get an attempt, got %+v", a)
		}
	}
	if len(verified) != 1 || verified[0] != "keep-me" {
		t.Fatalf("verified %v, want only keep-me", verified)
	}
	if ws.commitCalls != 1 {
		t.Fatalf("commit once per scanner turn, got %d", ws.commitCalls)
	}
}

func TestRun_PartialTurnSendsLeftoversBack(t *testing.T) {
	store := baseStore()
	store.findings["other"] = &models.Finding{
		ID: "other", FilePath: "other.go", Severity: models.SeverityHigh,
		ToolName: "gosec", RuleID: "Gx",
	}
	ws := &stubWorkspace{changed: []string{"main.go", "other.go"}}
	job := twoFindingJob()
	job.FindingIDList = []string{"keep-me", "other"}
	job.MaxAttempts = 1
	eng := &firstFileEngine{}
	var verified []string
	deps := Deps{
		Store:       store,
		Writability: passWritability{},
		Workspaces:  &stubPreparer{ws: ws},
		Engines: &chainSelector{build: func() EngineChain {
			return &stubChain{tiers: []engine.SubprocessEngine{eng}}
		}},
		Verifier: &verifyRecorder{pass: map[string]bool{"keep-me": true, "other": true}, seen: &verified},
		Diffs:    &stubDiffs{},
	}
	res, err := Run(context.Background(), job, deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.Kept != 2 {
		t.Fatalf("kept=%d want 2 (leftovers must be sent back)", res.Summary.Kept)
	}
	if len(eng.batches) < 2 {
		t.Fatalf("engine turns=%d want at least 2, batches=%v", len(eng.batches), eng.batches)
	}
	if eng.batches[0] != 2 || eng.batches[1] != 1 {
		t.Fatalf("batch sizes %v, want [2, 1, …]", eng.batches)
	}
	if len(verified) != 2 {
		t.Fatalf("verified %v, want both findings", verified)
	}
}

type stickyFileEngine struct {
	name string
	file string
}

func (e *stickyFileEngine) Name() string    { return e.name }
func (e *stickyFileEngine) Available() bool { return true }
func (e *stickyFileEngine) Fix(_ context.Context, _ engine.FixRequest) (*engine.FixResult, error) {
	return &engine.FixResult{Success: true, EditsInPlace: true, FilesChanged: []string{e.file}}, nil
}

type firstFileEngine struct {
	batches []int
}

func (e *firstFileEngine) Name() string    { return "opencode" }
func (e *firstFileEngine) Available() bool { return true }
func (e *firstFileEngine) Fix(_ context.Context, req engine.FixRequest) (*engine.FixResult, error) {
	batch := req.Batch()
	e.batches = append(e.batches, len(batch))
	if len(batch) == 0 || batch[0].FilePath == "" {
		return &engine.FixResult{Success: true, EditsInPlace: true}, nil
	}
	return &engine.FixResult{Success: true, EditsInPlace: true, FilesChanged: []string{batch[0].FilePath}}, nil
}

type verifyRecorder struct {
	pass       map[string]bool
	seen       *[]string
	batchCalls int
}

func (v *verifyRecorder) Verify(_ context.Context, _ verify.Workspace, f models.Finding) (*verify.VerifyResult, error) {
	if v.seen != nil {
		*v.seen = append(*v.seen, f.ID)
	}
	ok := v.pass[f.ID]
	return &verify.VerifyResult{Passed: ok, Built: true, FindingCleared: ok, ChangedFiles: []string{"main.go"}}, nil
}
func (v *verifyRecorder) VerifyBatch(ctx context.Context, ws verify.Workspace, findings []models.Finding) (map[string]*verify.VerifyResult, error) {
	v.batchCalls++
	out := make(map[string]*verify.VerifyResult, len(findings))
	for _, f := range findings {
		vr, err := v.Verify(ctx, ws, f)
		if err != nil {
			return out, err
		}
		out[f.ID] = vr
	}
	return out, nil
}

func TestRun_PushModePushesFixBranch(t *testing.T) {
	store := baseStore()
	ws := &stubWorkspace{changed: []string{"main.go"}}
	job := twoFindingJob()
	job.Mode = models.FixModePush
	job.MaxAttempts = 1
	job.FindingIDList = []string{"keep-me"}
	deps := Deps{
		Store:       store,
		Writability: passWritability{},
		Workspaces:  &stubPreparer{ws: ws},
		Engines: &chainSelector{build: func() EngineChain {
			return &stubChain{tiers: []engine.SubprocessEngine{&fixEngine{name: "claude-code"}}}
		}},
		Verifier: &stubVerifier{pass: map[string]bool{"keep-me": true}},
		Diffs:    &stubDiffs{},
	}
	res, err := Run(context.Background(), job, deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Summary.Pushed || ws.pushCalls != 1 {
		t.Fatalf("expected a push, got pushed=%v calls=%d", res.Summary.Pushed, ws.pushCalls)
	}
}

func TestRun_PushFailureKeepsFixesAndDoesNotFailJob(t *testing.T) {
	store := baseStore()
	ws := &stubWorkspace{
		changed: []string{"main.go"},
		pushErr: errors.New("refusing to allow a Personal Access Token to create or update workflow `.github/workflows/ci.yml` without `workflow` scope"),
	}
	job := twoFindingJob()
	job.Mode = models.FixModePush
	job.MaxAttempts = 1
	job.FindingIDList = []string{"keep-me"}
	deps := Deps{
		Store:       store,
		Writability: passWritability{},
		Workspaces:  &stubPreparer{ws: ws},
		Engines: &chainSelector{build: func() EngineChain {
			return &stubChain{tiers: []engine.SubprocessEngine{&fixEngine{name: "claude-code"}}}
		}},
		Verifier: &stubVerifier{pass: map[string]bool{"keep-me": true}},
		Diffs:    &stubDiffs{},
	}
	res, err := Run(context.Background(), job, deps)
	if err != nil {
		t.Fatalf("Run: %v (push failure must not fail the agent run)", err)
	}
	if res.PauseStatus != models.FixJobPushFailed {
		t.Fatalf("pause=%q want push_failed", res.PauseStatus)
	}
	if job.Status != models.FixJobPushFailed {
		t.Fatalf("job status=%q want push_failed", job.Status)
	}
	if job.Error == "" || !strings.Contains(job.PauseReason, "workflow") {
		t.Fatalf("expected push explanation, error=%q reason=%q", job.Error, job.PauseReason)
	}
	if res.Summary.Pushed || job.Pushed {
		t.Fatal("must not mark pushed")
	}
	if ws.cleanupCalls != 0 {
		t.Fatal("must keep the workspace after a push failure")
	}
}

type remainingRescanner struct{ keys map[string]bool }

func (r remainingRescanner) Rescan(context.Context, string, []string) ([]models.Finding, error) {
	var out []models.Finding
	for k := range r.keys {
		parts := strings.Split(k, "|")
		if len(parts) != 3 {
			continue
		}
		out = append(out, models.Finding{ToolName: parts[0], RuleID: parts[1], FilePath: parts[2]})
	}
	return out, nil
}

func TestRun_SkipNoiseMutes(t *testing.T) {
	store := baseStore()
	ws := &stubWorkspace{changed: []string{"main.go"}}
	job := twoFindingJob()
	job.FindingIDList = []string{"keep-me"}
	job.MaxAttempts = 1
	deps := Deps{
		Store:       store,
		Writability: passWritability{},
		Workspaces:  &stubPreparer{ws: ws},
		Engines: &chainSelector{build: func() EngineChain {
			return &stubChain{tiers: []engine.SubprocessEngine{skipNoiseEngine{}}}
		}},
		Verifier: &stubVerifier{pass: map[string]bool{}},
		Diffs:    &stubDiffs{},
	}
	res, err := Run(context.Background(), job, deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.Muted < 1 {
		t.Fatalf("expected mute, summary=%+v", res.Summary)
	}
	if len(store.suppressions) == 0 {
		t.Fatal("expected Wolf suppression for noise skip")
	}
}

func TestHygieneKindRouting(t *testing.T) {
	if hygiene.Classify("trivy") != hygiene.KindBump || hygiene.Classify("osv-scanner") != hygiene.KindBump {
		t.Fatal("cve scanners should bump")
	}
	if hygiene.Classify("scorecard") != hygiene.KindPolicy {
		t.Fatal("scorecard should be policy")
	}
	if hygiene.Classify("yamllint") != hygiene.KindLint {
		t.Fatal("yamllint should be lint")
	}
	if hygiene.Classify("gosec") != hygiene.KindCode || hygiene.Classify("bearer") != hygiene.KindCode {
		t.Fatal("sast scanners stay on the code agent")
	}
}

func TestTimeouts(t *testing.T) {
	if defaultPerToolTimeout != 25*time.Minute {
		t.Fatalf("per-tool = %s, want 25m", defaultPerToolTimeout)
	}
	if defaultJobWallClock != 6*time.Hour {
		t.Fatalf("wall = %s, want 6h", defaultJobWallClock)
	}
}

func TestRun_HygieneHandlesDumpScanners(t *testing.T) {
	store := baseStore()
	store.findings["cve"] = &models.Finding{
		ID: "cve", FilePath: "go.mod", Severity: models.SeverityCritical,
		ToolName: "trivy", RuleID: "CVE-2024-1", Title: "CVE-2024-1 in encoding/pem",
	}
	store.findings["policy"] = &models.Finding{
		ID: "policy", FilePath: "", Severity: models.SeverityHigh,
		ToolName: "scorecard", RuleID: "Branch-Protection",
	}
	ws := &stubWorkspace{changed: []string{"main.go"}}
	job := twoFindingJob()
	job.FindingIDList = []string{"keep-me", "cve", "policy"}
	job.MaxAttempts = 1
	var seen []string
	eng := &recordingEngine{tools: &seen, fixAll: true}
	deps := Deps{
		Store:       store,
		Writability: passWritability{},
		Workspaces:  &stubPreparer{ws: ws},
		Engines: &chainSelector{build: func() EngineChain {
			return &stubChain{tiers: []engine.SubprocessEngine{eng}}
		}},
		Verifier: &stubVerifier{pass: map[string]bool{"keep-me": true}},
		Diffs:    &stubDiffs{},
	}
	res, err := Run(context.Background(), job, deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, tool := range seen {
		if hygiene.Classify(tool) != hygiene.KindCode {
			t.Fatalf("hygiene scanner %q must not be sent to the code agent", tool)
		}
	}
	if res.Summary.Kept < 1 {
		t.Fatalf("expected gosec keep, summary=%+v", res.Summary)
	}
	if res.Summary.Muted < 2 {
		t.Fatalf("expected trivy+scorecard muted, summary=%+v", res.Summary)
	}
	if len(store.suppressions) != 0 {
		t.Fatalf("hygiene mutes must stay job-local, got %+v", store.suppressions)
	}
}

func TestRun_BatchVerifyOnceForToolTurn(t *testing.T) {
	store := &stubStore{
		repo:     &models.Repo{ID: "repo-1", SourceType: models.SourceTypeLocal, SourcePath: "/repo"},
		findings: map[string]*models.Finding{},
	}
	var ids []string
	for i := 0; i < 10; i++ {
		file := "a.go"
		if i >= 5 {
			file = "b.go"
		}
		id := fmt.Sprintf("f%02d", i)
		ids = append(ids, id)
		store.findings[id] = &models.Finding{
			ID: id, FilePath: file, Severity: models.SeverityHigh,
			ToolName: "gosec", RuleID: "G101",
		}
	}
	ws := &stubWorkspace{changed: []string{"a.go", "b.go"}}
	job := &models.FixJob{
		ID: "job-batch", RepoID: "repo-1", Type: "fix", Mode: models.FixModeDryRun,
		FindingIDList: ids, MaxAttempts: 1,
	}
	rec := &verifyRecorder{pass: map[string]bool{}, seen: new([]string)}
	for _, id := range ids {
		rec.pass[id] = true
	}
	deps := Deps{
		Store:       store,
		Writability: passWritability{},
		Workspaces:  &stubPreparer{ws: ws},
		Engines: &chainSelector{build: func() EngineChain {
			return &stubChain{tiers: []engine.SubprocessEngine{&recordingEngine{fixAll: true}}}
		}},
		Verifier: rec,
		Diffs:    &stubDiffs{},
	}
	res, err := Run(context.Background(), job, deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.batchCalls != 1 {
		t.Fatalf("VerifyBatch calls = %d, want 1", rec.batchCalls)
	}
	if res.Summary.Kept != 10 {
		t.Fatalf("kept=%d want 10", res.Summary.Kept)
	}
}

func TestRun_EmptyFilePathNotVerifiedOrRolledBack(t *testing.T) {
	store := baseStore()
	store.findings["empty"] = &models.Finding{
		ID: "empty", FilePath: "", Severity: models.SeverityHigh,
		ToolName: "gosec", RuleID: "G-empty",
	}
	ws := &stubWorkspace{changed: []string{"main.go"}}
	job := twoFindingJob()
	job.FindingIDList = []string{"keep-me", "empty"}
	job.MaxAttempts = 1
	var verified []string
	rec := &verifyRecorder{pass: map[string]bool{"keep-me": true}, seen: &verified}
	deps := Deps{
		Store:       store,
		Writability: passWritability{},
		Workspaces:  &stubPreparer{ws: ws},
		Engines: &chainSelector{build: func() EngineChain {
			return &stubChain{tiers: []engine.SubprocessEngine{&recordingEngine{fixAll: true}}}
		}},
		Verifier: rec,
		Diffs:    &stubDiffs{},
	}
	if _, err := Run(context.Background(), job, deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, id := range verified {
		if id == "empty" {
			t.Fatal("empty file_path must not be verified")
		}
	}
	for _, p := range ws.rolledBack {
		if p == "" {
			t.Fatal("empty file_path must not be rolled back")
		}
	}
}

func TestRun_StallMovesToNextScanner(t *testing.T) {
	store := baseStore()
	store.findings["other"] = &models.Finding{
		ID: "other", FilePath: "other.go", Severity: models.SeverityHigh,
		ToolName: "semgrep", RuleID: "S1",
	}
	ws := &stubWorkspace{changed: []string{"other.go"}}
	job := twoFindingJob()
	job.FindingIDList = []string{"keep-me", "other"}
	job.MaxAttempts = 1
	eng := &stallOnTool{stall: "gosec"}
	deps := Deps{
		Store:       store,
		Writability: passWritability{},
		Workspaces:  &stubPreparer{ws: ws},
		Engines: &chainSelector{build: func() EngineChain {
			return &stubChain{tiers: []engine.SubprocessEngine{eng}}
		}},
		Verifier: &stubVerifier{pass: map[string]bool{"other": true}},
		Diffs:    &stubDiffs{},
	}
	res, err := Run(context.Background(), job, deps)
	if err != nil {
		t.Fatalf("job must not fail on a stalled scanner: %v", err)
	}
	if !contains(eng.seen, "gosec") || !contains(eng.seen, "semgrep") {
		t.Fatalf("expected both tools, seen %v", eng.seen)
	}
	if res.Summary.Kept != 1 {
		t.Fatalf("kept=%d want 1 (semgrep)", res.Summary.Kept)
	}
}

func TestRun_LargeBatchIsOneFixTurn(t *testing.T) {
	store := &stubStore{
		repo:     &models.Repo{ID: "repo-1", SourceType: models.SourceTypeLocal, SourcePath: "/repo"},
		findings: map[string]*models.Finding{},
	}
	var ids []string
	for i := 0; i < 22; i++ {
		id := fmt.Sprintf("%08x-bbbb-cccc-dddd-000000000001", i)
		ids = append(ids, id)
		store.findings[id] = &models.Finding{
			ID: id, FilePath: fmt.Sprintf("s%d.go", i), Severity: models.SeverityHigh,
			ToolName: "gosec", RuleID: "G101",
		}
	}
	ws := &stubWorkspace{changed: []string{"s0.go"}}
	job := &models.FixJob{
		ID: "job-silent", RepoID: "repo-1", Type: "fix", Mode: models.FixModeDryRun,
		FindingIDList: ids, MaxAttempts: 1,
	}
	eng := &classifyEngine{silent: true}
	pass := map[string]bool{}
	for _, id := range ids {
		pass[id] = true
	}
	deps := Deps{
		Store:       store,
		Writability: passWritability{},
		Workspaces:  &stubPreparer{ws: ws},
		Engines: &chainSelector{build: func() EngineChain {
			return &stubChain{tiers: []engine.SubprocessEngine{eng}}
		}},
		Verifier: &stubVerifier{pass: pass},
		Diffs:    &stubDiffs{},
	}
	res, err := Run(context.Background(), job, deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(eng.phases) != 1 || eng.phases[0] != "fix" {
		t.Fatalf("phases = %v, want a single fix turn", eng.phases)
	}
	if len(eng.fixBatches) < 1 || len(eng.fixBatches[0]) != 22 {
		t.Fatalf("edit turn should see all path-backed ids, got %v", eng.fixBatches)
	}
	if res.Summary.Kept == 0 {
		t.Fatal("expected kept findings after the edit turn")
	}
	if len(res.Summary.Tools) == 0 || res.Summary.ReportMarkdown == "" {
		t.Fatalf("expected tool report, tools=%d md=%d", len(res.Summary.Tools), len(res.Summary.ReportMarkdown))
	}
}

func TestRun_HygieneMutedNotRetried(t *testing.T) {
	store := baseStore()
	store.findings["cve"] = &models.Finding{
		ID: "cve", FilePath: "go.mod", Severity: models.SeverityCritical,
		ToolName: "trivy", RuleID: "CVE-1", Title: "CVE-1 in encoding/json",
	}
	ws := &stubWorkspace{changed: []string{"main.go"}}
	job := twoFindingJob()
	job.FindingIDList = []string{"keep-me", "cve"}
	job.MaxAttempts = 1
	job.MaxLoops = 2
	var seen []string
	deps := Deps{
		Store:       store,
		Writability: passWritability{},
		Workspaces:  &stubPreparer{ws: ws},
		Engines: &chainSelector{build: func() EngineChain {
			return &stubChain{tiers: []engine.SubprocessEngine{&recordingEngine{tools: &seen, fixAll: true}}}
		}},
		Verifier: &stubVerifier{pass: map[string]bool{"keep-me": true}},
		Diffs:    &stubDiffs{},
		Rescan: remainingRescanner{keys: map[string]bool{
			"trivy|CVE-1|go.mod": true,
		}},
	}
	if _, err := Run(context.Background(), job, deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, tool := range seen {
		if hygiene.Classify(tool) != hygiene.KindCode {
			t.Fatalf("hygiene scanner %q must not be sent on any loop", tool)
		}
	}
}

func TestRun_UnableToVerifyKeepsEdits(t *testing.T) {
	store := baseStore()
	ws := &stubWorkspace{changed: []string{"main.go"}}
	job := twoFindingJob()
	job.FindingIDList = []string{"keep-me"}
	job.MaxAttempts = 1
	deps := Deps{
		Store:       store,
		Writability: passWritability{},
		Workspaces:  &stubPreparer{ws: ws},
		Engines: &chainSelector{build: func() EngineChain {
			return &stubChain{tiers: []engine.SubprocessEngine{&recordingEngine{fixAll: true}}}
		}},
		Verifier: &unverifiedVerifier{},
		Diffs:    &stubDiffs{},
	}
	res, err := Run(context.Background(), job, deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(ws.rolledBack) != 0 {
		t.Fatalf("missing scanner must not roll back, rolled=%v", ws.rolledBack)
	}
	if res.Summary.Kept != 1 {
		t.Fatalf("kept=%d want 1 (unverified keep)", res.Summary.Kept)
	}
}

type unverifiedVerifier struct{}

func (unverifiedVerifier) Verify(context.Context, verify.Workspace, models.Finding) (*verify.VerifyResult, error) {
	return &verify.VerifyResult{
		Passed: false, Built: true, FindingCleared: false,
		UnableToVerify: true, ChangedFiles: []string{"main.go"},
	}, nil
}
func (v unverifiedVerifier) VerifyBatch(ctx context.Context, ws verify.Workspace, findings []models.Finding) (map[string]*verify.VerifyResult, error) {
	out := make(map[string]*verify.VerifyResult, len(findings))
	for _, f := range findings {
		vr, err := v.Verify(ctx, ws, f)
		if err != nil {
			return out, err
		}
		out[f.ID] = vr
	}
	return out, nil
}

type skipNoiseEngine struct{}

func (e skipNoiseEngine) Name() string    { return "opencode" }
func (e skipNoiseEngine) Available() bool { return true }
func (e skipNoiseEngine) Fix(_ context.Context, req engine.FixRequest) (*engine.FixResult, error) {
	var b strings.Builder
	for _, f := range req.Batch() {
		fmt.Fprintf(&b, "SKIP: %s false positive\n", f.ID)
	}
	return &engine.FixResult{Success: true, EditsInPlace: true, Output: b.String()}, nil
}

func TestPersistDiffRestoresProtected(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("init")
	_ = os.MkdirAll(filepath.Join(dir, "chart", "charts"), 0o755)
	tgz := filepath.Join(dir, "chart", "charts", "argo-cd-9.5.21.tgz")
	_ = os.WriteFile(tgz, []byte("chart"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "docs", "openapi.yaml"), []byte("openapi: 3.0.0\n"), 0o644)
	run("add", ".")
	run("commit", "-m", "base")
	_ = os.Remove(tgz)
	_ = os.WriteFile(filepath.Join(dir, "docs", "openapi.yaml"), []byte("rewritten\n"), 0o644)

	job := &models.FixJob{ID: "job-protect"}
	diffs := &stubDiffs{}
	ws := &stubWorkspace{path: dir}
	if err := persistDiff(context.Background(), job, ws, Deps{Diffs: diffs}); err != nil {
		t.Fatalf("persistDiff: %v", err)
	}
	if _, err := os.Stat(tgz); err != nil {
		t.Fatal("helm tarball must be restored before the branch diff")
	}
	body, _ := os.ReadFile(filepath.Join(dir, "docs", "openapi.yaml"))
	if strings.Contains(string(body), "rewritten") {
		t.Fatalf("openapi rewritten in assembled diff workspace: %s", body)
	}
}

type recordingEngine struct {
	tools  *[]string
	ids    *[]string
	fixAll bool
}

func (e *recordingEngine) Name() string    { return "opencode" }
func (e *recordingEngine) Available() bool { return true }
func (e *recordingEngine) Fix(_ context.Context, req engine.FixRequest) (*engine.FixResult, error) {
	if e.tools != nil {
		*e.tools = append(*e.tools, req.Tool)
	}
	if e.ids != nil {
		for _, f := range req.Batch() {
			*e.ids = append(*e.ids, f.ID)
		}
	}
	var files []string
	var b strings.Builder
	seen := map[string]bool{}
	for _, f := range req.Batch() {
		if e.fixAll {
			fmt.Fprintf(&b, "FIX: %s patched\n", f.ID)
		}
		if f.FilePath != "" && !seen[f.FilePath] {
			seen[f.FilePath] = true
			files = append(files, f.FilePath)
		}
	}
	return &engine.FixResult{Success: true, EditsInPlace: true, FilesChanged: files, Output: b.String()}, nil
}

type stallOnTool struct {
	stall string
	seen  []string
}

func (e *stallOnTool) Name() string    { return "opencode" }
func (e *stallOnTool) Available() bool { return true }
func (e *stallOnTool) Fix(_ context.Context, req engine.FixRequest) (*engine.FixResult, error) {
	e.seen = append(e.seen, req.Tool)
	if req.Tool == e.stall {
		return &engine.FixResult{Error: engine.ErrStall.Error()}, nil
	}
	files := []string{"main.go"}
	if req.Finding.FilePath != "" {
		files = []string{req.Finding.FilePath}
	}
	var b strings.Builder
	for _, f := range req.Batch() {
		fmt.Fprintf(&b, "FIX: %s patched\n", f.ID)
	}
	return &engine.FixResult{Success: true, EditsInPlace: true, FilesChanged: files, Output: b.String()}, nil
}

type classifyEngine struct {
	fixFirst   int
	silent     bool
	phases     []string
	fixBatches [][]string
}

func (e *classifyEngine) Name() string    { return "opencode" }
func (e *classifyEngine) Available() bool { return true }
func (e *classifyEngine) Fix(_ context.Context, req engine.FixRequest) (*engine.FixResult, error) {
	phase := req.Phase
	if phase == "" {
		phase = "fix"
	}
	e.phases = append(e.phases, phase)
	batch := req.Batch()
	if phase == "classify" {
		if e.silent {
			return &engine.FixResult{Output: "looked at the review file\n"}, nil
		}
		var b strings.Builder
		for i, f := range batch {
			if i < e.fixFirst {
				fmt.Fprintf(&b, "FIX: %s would change\n", f.ID)
			} else {
				fmt.Fprintf(&b, "SKIP: %s noise\n", f.ID)
			}
		}
		return &engine.FixResult{Output: b.String()}, nil
	}
	var ids []string
	var files []string
	var b strings.Builder
	seen := map[string]bool{}
	for _, f := range batch {
		ids = append(ids, f.ID)
		fmt.Fprintf(&b, "FIX: %s patched\n", f.ID)
		if f.FilePath != "" && !seen[f.FilePath] {
			seen[f.FilePath] = true
			files = append(files, f.FilePath)
		}
	}
	e.fixBatches = append(e.fixBatches, ids)
	return &engine.FixResult{Success: true, EditsInPlace: true, FilesChanged: files, Output: b.String()}, nil
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
