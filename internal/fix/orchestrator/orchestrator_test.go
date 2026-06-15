package orchestrator

import (
	"context"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/fix/verify"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// --- stubs (no real agent CLIs, docker, or network) ---

// stubStore implements the orchestrator's Store interface in memory and records
// every FixAttempt + job update so tests can assert the audit trail.
type stubStore struct {
	repo     *models.Repo
	findings map[string]*models.Finding
	attempts []models.FixAttempt
	jobs     []models.FixJob // every UpdateFixJob snapshot, in order
}

func (s *stubStore) GetRepoByID(_ context.Context, _ string) (*models.Repo, error) {
	return s.repo, nil
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

// stubWorkspace is a fake working tree. It tracks rollbacks and pushes; the
// orchestrator must NEVER push (v1 is branch-only), so pushes is asserted zero.
type stubWorkspace struct {
	branch       string
	changed      []string
	rolledBack   []string
	cleanupCalls int
	pushCalls    int // must stay 0
}

func (w *stubWorkspace) Path() string { return "/ws" }
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

// fixEngine is a stub SubprocessEngine whose Fix returns a canned in-place edit
// result. name distinguishes tiers in the chain.
type fixEngine struct {
	name string
	fail bool // when true, Fix reports no successful fix
}

func (e *fixEngine) Name() string    { return e.name }
func (e *fixEngine) Available() bool { return true }
func (e *fixEngine) Fix(context.Context, engine.FixRequest) (*engine.FixResult, error) {
	if e.fail {
		return &engine.FixResult{Success: false, Error: "engine could not fix"}, nil
	}
	return &engine.FixResult{Success: true, EditsInPlace: true, FilesChanged: []string{"main.go"}}, nil
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
			"escalate-me": {ID: "escalate-me", FilePath: "util.go", Severity: models.SeverityHigh, ToolName: "gosec", RuleID: "G2"},
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

	// --- NO push (v1 is branch-only). ---
	if ws.pushCalls != 0 {
		t.Fatalf("expected zero pushes, got %d", ws.pushCalls)
	}
	// Workspace cleaned up (branch survives; the worktree bookkeeping does not).
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
