package orchestrator

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/fix/verify"
	"github.com/alphabravocompany/thewolf/internal/fix/workspace"
	"github.com/alphabravocompany/thewolf/internal/models"
)

type failingAttemptStore struct {
	*stubStore
	err error
}

func (s failingAttemptStore) CreateFixAttempt(ctx context.Context, att *models.FixAttempt) error {
	if att.Outcome == models.FixOutcomeRunning {
		return s.stubStore.CreateFixAttempt(ctx, att)
	}
	return s.err
}
func (s failingAttemptStore) UpdateFixAttempt(context.Context, *models.FixAttempt) error {
	return s.err
}

func TestAttemptPersistenceFailureStopsPublication(t *testing.T) {
	for _, outcome := range []string{"kept", "rolled_back", "muted", "hygiene"} {
		t.Run(outcome, func(t *testing.T) {
			failure := errors.New("attempt database unavailable")
			store := failingAttemptStore{baseStore(), failure}
			finding := *store.findings["keep-me"]
			finding.ID = "abcdef01"
			store.findings = map[string]*models.Finding{finding.ID: &finding}
			ws := &stubWorkspace{path: t.TempDir(), changed: []string{"main.go"}}
			job := twoFindingJob()
			job.Mode, job.FindingIDList = models.FixModePush, []string{finding.ID}
			var editor engine.SubprocessEngine = &fixEngine{}
			if outcome == "muted" {
				editor = skipNoiseEngine{}
				ws.changed = nil
			}
			if outcome == "hygiene" {
				finding.ToolName = "scorecard"
			}
			_, err := Run(t.Context(), job, Deps{Store: store, Workspaces: &stubPreparer{ws: ws},
				Engines:  &chainSelector{build: func() EngineChain { return &stubChain{tiers: []engine.SubprocessEngine{editor}} }},
				Verifier: &stubVerifier{pass: map[string]bool{finding.ID: outcome != "rolled_back"}}, Diffs: &stubDiffs{},
			})
			if !errors.Is(err, failure) || job.Status != models.FixJobFailed || job.WorkspacePath != ws.path {
				t.Fatalf("history failure ignored: job=%+v err=%v", job, err)
			}
			if ws.commitCalls != 0 || ws.pushCalls != 0 || ws.cleanupCalls != 0 {
				t.Fatalf("continued without durable history: %+v", ws)
			}
		})
	}
}

type failingRescan struct{ err error }

func (s failingRescan) Rescan(context.Context, string, []string) ([]models.Finding, error) {
	return nil, s.err
}

func TestBranchRescanFailureStopsPublication(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "after-edits", true: "resume"}[resume], func(t *testing.T) {
			failure := errors.New("scanner transport failed")
			store := baseStore()
			ws := &stubWorkspace{path: t.TempDir(), changed: []string{"main.go"}}
			job := twoFindingJob()
			job.Mode, job.FindingIDList = models.FixModePush, []string{"keep-me"}
			if resume {
				job.CurrentLoop, job.WorkspacePath = 1, ws.path
			}
			_, err := Run(t.Context(), job, Deps{Store: store, Workspaces: &stubPreparer{ws: ws},
				Engines:  &chainSelector{build: func() EngineChain { return &stubChain{tiers: []engine.SubprocessEngine{&fixEngine{}}} }},
				Verifier: &stubVerifier{pass: map[string]bool{"keep-me": true}}, Diffs: &stubDiffs{}, Rescan: failingRescan{failure},
			})
			if !errors.Is(err, failure) || !strings.Contains(job.Error, "branch rescan failed") || job.Status != models.FixJobFailed {
				t.Fatalf("rescan failure accepted: job=%+v err=%v", job, err)
			}
			if ws.pushCalls != 0 || ws.cleanupCalls != 0 || job.WorkspacePath != ws.path {
				t.Fatalf("unverified branch published or removed: %+v", ws)
			}
			if resume && len(store.attempts) != 0 {
				t.Fatal("resume ran an engine despite failed initial rescan")
			}
		})
	}
}

type crossFileBuildVerifier struct {
	stubVerifier
	builds []bool
}

func (v *crossFileBuildVerifier) VerifyBatch(ctx context.Context, ws verify.Workspace, findings []models.Finding) (map[string]*verify.VerifyResult, error) {
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = ws.Path()
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOTOOLCHAIN=local")
	output, err := cmd.CombinedOutput()
	built := err == nil
	v.builds = append(v.builds, built)
	result := map[string]*verify.VerifyResult{}
	for _, f := range findings {
		cleared := f.ID == "keep-me"
		result[f.ID] = &verify.VerifyResult{Passed: built && cleared, Built: built, FindingCleared: cleared,
			Stages: []verify.StageResult{{Stage: verify.StageBuild, Passed: built, Detail: string(output)}},
		}
	}
	return result, nil
}

func TestSelectiveRollbackRechecksCrossFileBuild(t *testing.T) {
	ctx := t.Context()
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.test", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	for name, content := range map[string]string{"go.mod": "module fixture\n\ngo 1.26\n", "a.go": "package fixture\nfunc use() {}\n", "b.go": "package fixture\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-b", "main")
	git("add", ".")
	git("commit", "-m", "baseline")
	store := baseStore()
	store.repo.SourcePath = repo
	store.findings["keep-me"].FilePath = "a.go"
	store.findings["escalate-me"].FilePath = "b.go"
	ws, err := workspace.Prepare(ctx, workspace.Options{Repo: store.repo, Branch: "wolf-fix/cross-file", BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	editor := turnEngine(func(engine.FixRequest) (*engine.FixResult, error) {
		for name, content := range map[string]string{"a.go": "package fixture\nfunc use() { added() }\n", "b.go": "package fixture\nfunc added() {}\n"} {
			if err := os.WriteFile(filepath.Join(ws.Path(), name), []byte(content), 0o600); err != nil {
				return nil, err
			}
		}
		return &engine.FixResult{EditsInPlace: true, FilesChanged: []string{"a.go", "b.go"}}, nil
	})
	verifier := &crossFileBuildVerifier{}
	job := twoFindingJob()
	job.Mode = models.FixModePush
	_, err = Run(ctx, job, Deps{Store: store, Workspaces: fixedPreparer{ws}, Diffs: &stubDiffs{}, Verifier: verifier,
		Engines: &chainSelector{build: func() EngineChain { return &stubChain{tiers: []engine.SubprocessEngine{editor}} }},
	})
	if err == nil || !strings.Contains(err.Error(), "combined verification after rollback failed") || job.Status != models.FixJobFailed {
		t.Fatalf("broken surviving branch accepted: %+v %v", job, err)
	}
	if len(verifier.builds) != 2 || !verifier.builds[0] || verifier.builds[1] {
		t.Fatalf("expected build to break only after rollback: %v", verifier.builds)
	}
	for _, attempt := range store.attempts {
		if attempt.Outcome == models.FixOutcomeKept {
			t.Fatalf("pre-rollback build claimed as final evidence: %+v", attempt)
		}
	}
	changed, err := ws.ChangedFiles(ctx)
	if err != nil || len(changed) != 1 || changed[0] != "a.go" || job.Pushed || job.WorkspacePath != ws.Path() {
		t.Fatalf("uncommitted recovery work lost: %v %v %+v", changed, err, job)
	}
}
