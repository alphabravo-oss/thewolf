package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/fix/verify"
	"github.com/alphabravocompany/thewolf/internal/fix/workspace"
	"github.com/alphabravocompany/thewolf/internal/models"
)

type finalBranchVerifier struct {
	stubVerifier
	scan func(context.Context, string, []string) ([]models.Finding, error)
}

func (v *finalBranchVerifier) Rescan(ctx context.Context, path string, tools []string) ([]models.Finding, error) {
	return v.scan(ctx, path, tools)
}

func (v *finalBranchVerifier) VerifyBranch(ctx context.Context, ws verify.Workspace, before, cleared []models.Finding, tools []string) ([]verify.StageResult, error) {
	return verify.GateBranch(ctx, ws, before, cleared, tools, v)
}

type publicationWorkspace struct {
	Workspace
	pushes, cleanups int
}

func (w *publicationWorkspace) Push(ctx context.Context) (string, error) {
	w.pushes++
	return w.Head(ctx)
}

func (w *publicationWorkspace) Cleanup(ctx context.Context) error {
	w.cleanups++
	return w.Workspace.Cleanup(ctx)
}

func TestFinalBranchVerificationAfterToolCommits(t *testing.T) {
	for _, mode := range []string{"valid", "earlier-fix-undone", "extra-file", "scanner-unavailable", "scanner-mutates-tree"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			repo := t.TempDir()
			git := func(args ...string) {
				t.Helper()
				cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
				cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %s %v", args, out, err)
				}
			}
			git("init", "-b", "main")
			git("commit", "--allow-empty", "-m", "baseline")
			store := baseStore()
			store.repo.SourcePath = repo
			store.scan = &models.Scan{ID: "scan", RepoID: store.repo.ID, ToolsSelected: `["gosec","semgrep","clean-tool"]`, ToolsCompleted: `["gosec","semgrep"]`}
			store.findings["keep-me"].FilePath = "a.txt"
			store.findings["keep-me"].Severity = models.SeverityCritical
			store.findings["escalate-me"].FilePath = "b.txt"
			store.findings["escalate-me"].ToolName = "semgrep"
			known := models.Finding{ID: "not-selected", ScanID: "scan", ToolName: "semgrep", RuleID: "known", FilePath: "other.txt"}
			store.findings[known.ID] = &known
			prepared, err := workspace.Prepare(ctx, workspace.Options{Repo: store.repo, Branch: "wolf-fix/final", BaseDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			ws := &publicationWorkspace{Workspace: prepared}
			editor := turnEngine(func(req engine.FixRequest) (*engine.FixResult, error) {
				if err := os.WriteFile(filepath.Join(req.RepoPath, req.Finding.FilePath), []byte("fixed"), 0o600); err != nil {
					return nil, err
				}
				if req.Tool == "semgrep" {
					file, content := "extra.txt", "safe extra edit"
					if mode == "earlier-fix-undone" {
						file, content = "a.txt", "unsafe"
					} else if mode == "extra-file" {
						content = "unsafe"
					}
					if err := os.WriteFile(filepath.Join(req.RepoPath, file), []byte(content), 0o600); err != nil {
						return nil, err
					}
				}
				return &engine.FixResult{EditsInPlace: true}, nil
			})
			calls := 0
			verifier := &finalBranchVerifier{stubVerifier: stubVerifier{pass: map[string]bool{"keep-me": true, "escalate-me": true}}}
			verifier.scan = func(_ context.Context, path string, tools []string) ([]models.Finding, error) {
				calls++
				if !slices.Equal(tools, []string{"clean-tool", "gosec", "semgrep"}) {
					t.Fatalf("lost original zero-finding tool: %v", tools)
				}
				if mode == "scanner-unavailable" {
					return nil, errors.New("scanner unavailable")
				}
				if mode == "scanner-mutates-tree" {
					if err := os.WriteFile(filepath.Join(path, "after-check.txt"), []byte("unverified"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				post := []models.Finding{known}
				if data, _ := os.ReadFile(filepath.Join(path, "a.txt")); string(data) == "unsafe" {
					post = append(post, *store.findings["keep-me"])
				}
				if data, _ := os.ReadFile(filepath.Join(path, "extra.txt")); string(data) == "unsafe" {
					post = append(post, models.Finding{ToolName: "clean-tool", RuleID: "new", FilePath: "extra.txt"})
				}
				return post, nil
			}
			job := twoFindingJob()
			job.ScanID, job.Mode, job.WorkspacePath = "scan", models.FixModePush, ws.Path()
			result, err := Run(ctx, job, Deps{Store: store, Workspaces: fixedPreparer{ws}, Verifier: verifier, Diffs: &stubDiffs{},
				Engines: &chainSelector{build: func() EngineChain { return &stubChain{tiers: []engine.SubprocessEngine{editor}} }},
			})
			if calls != 1 {
				t.Fatalf("final gate not run exactly once: %d %v", calls, err)
			}
			if mode == "valid" {
				if err != nil || ws.pushes != 1 || job.Status != models.FixJobSucceeded || len(result.Summary.FinalVerification) == 0 {
					t.Fatalf("valid branch failed: job=%+v err=%v", job, err)
				}
			} else if err == nil || ws.pushes != 0 || ws.cleanups != 0 || job.WorkspacePath != ws.Path() || job.Status != models.FixJobFailed {
				t.Fatalf("unsafe branch published or lost: job=%+v pushes=%d cleanup=%d err=%v", job, ws.pushes, ws.cleanups, err)
			}
		})
	}
}

func TestPublicationResumeRechecksBranch(t *testing.T) {
	for _, mode := range []string{"regression", "continued-run-regression", "changed-published-head"} {
		t.Run(mode, func(t *testing.T) {
			store := baseStore()
			ws := &stubWorkspace{path: t.TempDir(), branch: "wolf-fix/resume", changed: []string{"extra.txt"}}
			job := twoFindingJob()
			job.WorkspacePath, job.ResumeAction = ws.path, models.FixResumePush
			if mode == "continued-run-regression" {
				job.CurrentLoop, job.MaxLoops, job.ResumeAction = 1, 1, models.FixResumeContinue
			}
			summary := Summary{Branch: ws.branch, Kept: 1, Addressed: []FindingNote{{ID: "keep-me"}}}
			data, _ := json.Marshal(summary)
			job.Summary = string(data)
			verifier := &finalBranchVerifier{}
			verifier.scan = func(context.Context, string, []string) ([]models.Finding, error) {
				if strings.Contains(mode, "regression") {
					return []models.Finding{*store.findings["keep-me"]}, nil
				}
				return nil, nil
			}
			if mode == "changed-published-head" {
				job.Pushed, job.PushSHA = true, "old-published-head"
				ws.changed = nil
			}
			_, err := Run(t.Context(), job, Deps{Store: store, Workspaces: &stubPreparer{ws: ws}, Verifier: verifier, Engines: &chainSelector{}, Diffs: &stubDiffs{}})
			if err == nil || !strings.Contains(err.Error(), "workspace retained") || ws.pushCalls != 0 || ws.cleanupCalls != 0 || len(store.attempts) != 0 {
				t.Fatalf("unsafe publication retry: %v %+v", err, ws)
			}
		})
	}
}

func TestFinalBranchIncludesEarlierRoundClearances(t *testing.T) {
	store := baseStore()
	store.findings["keep-me"].FilePath = "first.txt"
	store.findings["escalate-me"].FilePath = "second.txt"
	ws := &stubWorkspace{path: t.TempDir(), changed: []string{"first.txt", "second.txt"}}
	v := &finalBranchVerifier{scan: func(context.Context, string, []string) ([]models.Finding, error) {
		return []models.Finding{*store.findings["escalate-me"]}, nil
	}}
	summary := Summary{Kept: 1, Addressed: []FindingNote{{ID: "keep-me"}}, Rounds: []RoundSummary{{Cleared: []string{"escalate-me"}}}}
	_, err := verifyFinalBranch(t.Context(), twoFindingJob(), ws, Deps{Store: store, Verifier: v}, summary)
	if err == nil || !strings.Contains(err.Error(), "previously accepted") {
		t.Fatalf("later tool could undo an earlier round clearance: %v", err)
	}
}
