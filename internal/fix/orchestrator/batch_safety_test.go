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

type turnEngine func(engine.FixRequest) (*engine.FixResult, error)

func (turnEngine) Name() string    { return "test" }
func (turnEngine) Available() bool { return true }
func (e turnEngine) Fix(_ context.Context, r engine.FixRequest) (*engine.FixResult, error) {
	return e(r)
}

type fixedPreparer struct{ Workspace }

func (p fixedPreparer) Prepare(context.Context, *models.Repo, string, string) (Workspace, error) {
	return p.Workspace, nil
}
func (p fixedPreparer) Open(context.Context, string, *models.Repo) (Workspace, error) {
	return p.Workspace, nil
}

func TestRunSharedFileRollbackMatchesAttemptsAndDiff(t *testing.T) {
	for _, mode := range []string{"pass-first", "pass-last", "omitted-file-claim", "agent-commit"} {
		t.Run(mode, func(t *testing.T) {
			repo := t.TempDir()
			git := func(args ...string) {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
				cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %s: %v", args, out, err)
				}
			}
			git("init")
			const original = "prior committed repair\nA=unsafe\nB=unsafe\n"
			if err := os.WriteFile(filepath.Join(repo, "source.txt"), []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			git("add", ".")
			git("commit", "-m", "prior repair")
			store := baseStore()
			store.repo.SourcePath = repo
			store.findings = map[string]*models.Finding{
				"abcdef01": {ID: "abcdef01", Severity: models.SeverityHigh, ToolName: "gosec", RuleID: "G1"},
				"abcdef02": {ID: "abcdef02", Severity: models.SeverityHigh, ToolName: "gosec", RuleID: "G2"},
			}
			for _, f := range store.findings {
				f.FilePath = "source.txt"
			}
			ws, err := workspace.Prepare(t.Context(), workspace.Options{Repo: store.repo, Branch: "wolf-fix/test", BaseDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ws.Cleanup(context.Background()) })
			calls := 0
			editor := turnEngine(func(req engine.FixRequest) (*engine.FixResult, error) {
				calls++
				if err := os.WriteFile(filepath.Join(req.RepoPath, "source.txt"), []byte("prior committed repair\nA=safe\nB=still-unsafe\n"), 0600); err != nil {
					return nil, err
				}
				if mode == "omitted-file-claim" && calls == 1 {
					return &engine.FixResult{EditsInPlace: true, Output: "FIX: abcdef01 repaired A"}, nil
				}
				if mode == "agent-commit" {
					cmd := exec.CommandContext(t.Context(), "git", "-C", req.RepoPath, "-c", "user.name=test", "-c", "user.email=test@test", "commit", "-am", "agent repair")
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("agent commit: %s %v", out, err)
					}
				}
				return &engine.FixResult{EditsInPlace: true, FilesChanged: []string{"source.txt"}}, nil
			})
			pass := "abcdef01"
			if mode == "pass-last" {
				pass = "abcdef02"
			}
			job := twoFindingJob()
			job.FindingIDList = []string{"abcdef01", "abcdef02"}
			job.WorkspacePath = ws.Path() // Retain the actual tree for inspection.
			job.MaxAttempts = 1
			deps := Deps{Store: store, Workspaces: fixedPreparer{ws}, Diffs: &stubDiffs{},
				Engines:  &chainSelector{build: func() EngineChain { return &stubChain{tiers: []engine.SubprocessEngine{editor}} }},
				Verifier: &stubVerifier{pass: map[string]bool{pass: true}},
			}
			res, err := Run(t.Context(), job, deps)
			if err != nil {
				t.Fatal(err)
			}
			if res.Summary.Kept != 0 {
				t.Fatalf("reported erased repair as kept: %+v", res.Summary)
			}
			latest := map[string]string{}
			for _, att := range store.attempts {
				latest[att.FindingID] = att.Outcome
			}
			for id := range store.findings {
				if latest[id] == "" || latest[id] == models.FixOutcomeKept {
					t.Fatalf("wrong final attempt for %s: %v", id, latest)
				}
			}
			content, err := os.ReadFile(filepath.Join(ws.Path(), "source.txt"))
			if err != nil || string(content) != original {
				t.Fatalf("rollback lost committed repair: %q: %v", content, err)
			}
			changed, err := ws.ChangedFiles(t.Context())
			if err != nil || len(changed) != 0 {
				t.Fatalf("unexpected final diff: %v: %v", changed, err)
			}
			if mode == "omitted-file-claim" && calls != 1 {
				t.Fatalf("omitted file claim hid the first-turn rollback: %d calls", calls)
			}
		})
	}
}

type unavailableBatch struct {
	stubVerifier
	mode string
}

func (v unavailableBatch) VerifyBatch(ctx context.Context, ws verify.Workspace, fs []models.Finding) (map[string]*verify.VerifyResult, error) {
	batch, _ := v.stubVerifier.VerifyBatch(ctx, ws, fs)
	switch v.mode {
	case "error":
		return batch, errors.New("scanner transport failed")
	case "missing-result":
		delete(batch, fs[len(fs)-1].ID)
	case "unavailable":
		batch[fs[len(fs)-1].ID].UnableToVerify = true
	}
	return batch, nil
}

func TestRunIncompleteBatchNeverRecordsKeepOrPushes(t *testing.T) {
	for _, mode := range []string{"error", "missing-result", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			store := baseStore()
			ws := &stubWorkspace{path: t.TempDir(), changed: []string{"main.go"}}
			job := twoFindingJob()
			job.Mode = models.FixModePush
			deps := Deps{Store: store, Workspaces: &stubPreparer{ws: ws}, Diffs: &stubDiffs{},
				Engines:  &chainSelector{build: func() EngineChain { return &stubChain{tiers: []engine.SubprocessEngine{&fixEngine{}}} }},
				Verifier: &unavailableBatch{stubVerifier{pass: map[string]bool{"keep-me": true, "escalate-me": true}}, mode},
			}
			_, err := Run(t.Context(), job, deps)
			if err == nil || !strings.Contains(err.Error(), "verification incomplete") {
				t.Fatalf("missing verification succeeded: %v", err)
			}
			if ws.commitCalls != 0 || ws.pushCalls != 0 || ws.cleanupCalls != 0 || len(ws.rolledBack) != 0 {
				t.Fatalf("incomplete batch changed or deleted: %+v", ws)
			}
			for _, att := range store.attempts {
				if att.Outcome == models.FixOutcomeKept {
					t.Fatalf("partial batch recorded keep: %+v", att)
				}
			}
		})
	}
}

func TestRunAppliesBatchPatchOnceAndStopsOnFailure(t *testing.T) {
	for _, mode := range []string{"success", "apply-error", "missing-applier", "engine-error"} {
		t.Run(mode, func(t *testing.T) {
			store := baseStore()
			ws := &stubWorkspace{path: t.TempDir(), changed: []string{"main.go"}}
			job := twoFindingJob()
			applier := &recordingApplier{}
			if mode == "apply-error" {
				applier.err = errors.New("patch rejected")
			}
			var eng engine.SubprocessEngine = &apiDiffEngine{}
			if mode == "engine-error" {
				eng = turnEngine(func(engine.FixRequest) (*engine.FixResult, error) { return nil, errors.New("engine disconnected") })
			}
			deps := Deps{Store: store, Workspaces: &stubPreparer{ws: ws}, Diffs: &stubDiffs{}, GitApply: applier,
				Engines:  &chainSelector{build: func() EngineChain { return &stubChain{tiers: []engine.SubprocessEngine{eng}} }},
				Verifier: &stubVerifier{pass: map[string]bool{"keep-me": true, "escalate-me": true}},
			}
			if mode == "missing-applier" {
				deps.GitApply = nil
			}
			res, err := Run(t.Context(), job, deps)
			if mode == "success" {
				if err != nil || res.Summary.Kept != 2 || applier.calls != 1 {
					t.Fatalf("batch patch not applied exactly once: %+v %v calls=%d", res, err, applier.calls)
				}
			} else {
				if err == nil || store.jobs[len(store.jobs)-1].Status != models.FixJobFailed {
					t.Fatalf("failure reported success: %v", err)
				}
				if ws.commitCalls != 0 || ws.pushCalls != 0 || ws.cleanupCalls != 0 {
					t.Fatalf("failed edits committed or removed: %+v", ws)
				}
			}
		})
	}
}

func TestFormatChangedGoConfinesAgentPaths(t *testing.T) {
	repo, outside := t.TempDir(), t.TempDir()
	const source = "package demo;var X=1\n"
	victim := filepath.Join(outside, "victim.go")
	if err := os.WriteFile(victim, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(repo, ".git", "metadata.go")
	if err := os.WriteFile(metadata, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"normal.go", "-flag.go", "space name.go", "line\nbreak.go"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	rel, err := filepath.Rel(repo, victim)
	if err != nil {
		t.Fatal(err)
	}
	files := []string{victim, rel, "escape/victim.go", ".git/metadata.go", "deleted.go", "normal.go", "-flag.go", "space name.go", "line\nbreak.go"}
	if err := formatChangedGo(t.Context(), repo, files); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{victim, metadata} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != source {
			t.Fatalf("formatter modified forbidden file %q: %q %v", path, got, err)
		}
	}
	for _, name := range files[5:] {
		got, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil || !strings.Contains(string(got), "var X = 1") {
			t.Fatalf("literal file not formatted %q: %q %v", name, got, err)
		}
	}
}

func TestRunIgnoresDecisionsForUnknownFindings(t *testing.T) {
	store := baseStore()
	job := twoFindingJob()
	job.FindingIDList = []string{"keep-me"}
	ws := &stubWorkspace{path: t.TempDir()}
	eng := turnEngine(func(engine.FixRequest) (*engine.FixResult, error) {
		return &engine.FixResult{EditsInPlace: true, Output: "SKIP: deadbeef false positive"}, nil
	})
	res, err := Run(t.Context(), job, Deps{Store: store, Workspaces: &stubPreparer{ws: ws}, Diffs: &stubDiffs{},
		Engines:  &chainSelector{build: func() EngineChain { return &stubChain{tiers: []engine.SubprocessEngine{eng}} }},
		Verifier: &stubVerifier{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Muted != 0 || res.Summary.Unfixable != 0 || len(store.suppressions) != 0 {
		t.Fatalf("unknown decision applied to sole finding: %+v %+v", res.Summary, store.suppressions)
	}
}

func TestRunRetryUpdatesReportToFinalVerdict(t *testing.T) {
	store := baseStore()
	job := twoFindingJob()
	job.FindingIDList = []string{"keep-me"}
	ws := &stubWorkspace{path: t.TempDir(), changed: []string{"main.go"}}
	verifier := &stubVerifier{pass: map[string]bool{}}
	calls := 0
	eng := turnEngine(func(engine.FixRequest) (*engine.FixResult, error) {
		calls++
		verifier.pass["keep-me"] = calls == 2
		return &engine.FixResult{EditsInPlace: true, FilesChanged: []string{"main.go"}}, nil
	})
	res, err := Run(t.Context(), job, Deps{Store: store, Workspaces: &stubPreparer{ws: ws}, Diffs: &stubDiffs{}, Verifier: verifier,
		Engines: &chainSelector{build: func() EngineChain { return &stubChain{tiers: []engine.SubprocessEngine{eng, eng}} }},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Kept != 1 || len(res.Summary.Open) != 0 || len(res.Summary.Tools) != 1 || res.Summary.Tools[0].Kept != 1 || res.Summary.Tools[0].Open != 0 {
		t.Fatalf("report retained obsolete rollback outcome: %+v", res.Summary)
	}
}
