package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/fix/pr"
	"github.com/alphabravocompany/thewolf/internal/fix/verify"
	"github.com/alphabravocompany/thewolf/internal/models"
)

type failedDiffs struct{ err error }

func (d failedDiffs) SaveDiff(context.Context, string, string) (string, error) { return "", d.err }

func TestRunRetainsWorkspaceWhenDiffStorageFails(t *testing.T) {
	for _, stage := range []string{"live", "review", "final"} {
		t.Run(stage, func(t *testing.T) {
			store := baseStore()
			ws := &stubWorkspace{path: t.TempDir(), changed: []string{"main.go"}}
			job := twoFindingJob()
			job.FindingIDList = []string{"keep-me"}
			job.Mode = models.FixModePush
			job.MaxAttempts = 1
			if stage == "review" {
				job.MaxLoops = 2
				job.HumanInTheLoop = true
			}
			failure := errors.New("artifact store unavailable")
			deps := Deps{Store: store, Workspaces: &stubPreparer{ws: ws}, Diffs: failedDiffs{failure},
				Engines: &chainSelector{build: func() EngineChain {
					return &stubChain{tiers: []engine.SubprocessEngine{&fixEngine{fail: stage != "live"}}}
				}},
				Verifier: &stubVerifier{pass: map[string]bool{"keep-me": true}},
			}
			_, err := Run(t.Context(), job, deps)
			if !errors.Is(err, failure) || !strings.Contains(err.Error(), "workspace retained") {
				t.Fatalf("artifact failure lost: %v", err)
			}
			if ws.cleanupCalls != 0 || ws.pushCalls != 0 {
				t.Fatalf("unreviewable work pushed or removed: %+v", ws)
			}
			if job.Status != models.FixJobFailed || job.WorkspacePath != ws.path {
				t.Fatalf("job lost failed workspace: %+v", job)
			}
		})
	}
}

type reopenFailure struct {
	prepareCalls int
	err          error
}

func (p *reopenFailure) Prepare(context.Context, *models.Repo, string, string) (Workspace, error) {
	p.prepareCalls++
	return &stubWorkspace{}, nil
}
func (p *reopenFailure) Open(context.Context, string, *models.Repo) (Workspace, error) {
	return nil, p.err
}

func TestResumeNeverReplacesAnUnreadableWorkspace(t *testing.T) {
	for _, err := range []error{errors.New("workspace metadata unreadable"), nil} {
		p := &reopenFailure{err: err}
		job := twoFindingJob()
		job.WorkspacePath = "/retained/repair"
		_, _, got := openOrPrepare(t.Context(), job, baseStore().repo, "wolf-fix/resume", Deps{Workspaces: p})
		if got == nil || !strings.Contains(got.Error(), "reopen retained workspace") || p.prepareCalls != 0 || job.WorkspacePath != "/retained/repair" {
			t.Fatalf("resume silently replaced the retained workspace: %v %+v %+v", got, p, job)
		}
	}
}

func TestRunStopsAndRetainsWorkspaceOnRecoveryFailure(t *testing.T) {
	for _, name := range []string{"rollback", "commit"} {
		t.Run(name, func(t *testing.T) {
			store := baseStore()
			ws := &stubWorkspace{path: t.TempDir(), branch: "wolf-fix/recovery", changed: []string{"main.go"}}
			failure := errors.New("index is locked")
			if name == "rollback" {
				ws.rollbackErr = failure
			} else {
				ws.commitErr = failure
			}
			job := twoFindingJob()
			job.FindingIDList = []string{"keep-me"}
			job.Mode = models.FixModePush
			deps := Deps{
				Store: store, Workspaces: &stubPreparer{ws: ws},
				Engines: &chainSelector{build: func() EngineChain {
					return &stubChain{tiers: []engine.SubprocessEngine{&fixEngine{name: "first"}, &fixEngine{name: "second"}}}
				}},
				Verifier: &stubVerifier{pass: map[string]bool{"keep-me": name == "commit"}},
				Diffs:    &stubDiffs{},
			}
			_, err := Run(t.Context(), job, deps)
			if !errors.Is(err, failure) {
				t.Fatalf("recovery failure did not reach caller: %v", err)
			}
			final := store.jobs[len(store.jobs)-1]
			if final.Status != models.FixJobFailed || !strings.Contains(final.Error, "workspace retained") || final.WorkspacePath != ws.path {
				t.Fatalf("failed job lost recovery evidence: %#v", final)
			}
			if ws.cleanupCalls != 0 || ws.pushCalls != 0 {
				t.Fatalf("failed workspace cleaned or pushed: %+v", ws)
			}
			if len(store.attempts) != 1 {
				t.Fatalf("execution continued after recovery failure: %+v", store.attempts)
			}
			if name == "rollback" {
				if ws.commitCalls != 0 || store.attempts[0].Outcome == models.FixOutcomeRolledBack || !strings.Contains(store.attempts[0].DiffExcerpt, "rollback failed") {
					t.Fatalf("failed rollback reported success or committed: %+v %+v", ws, store.attempts)
				}
			}
		})
	}
}

func TestPublishResumePreservesEvidenceAndReportsPRFailures(t *testing.T) {
	original := githubPR
	t.Cleanup(func() { githubPR = original })
	for _, failure := range []string{"", "error", "result_error", "nil", "empty", "bad_summary", "already_pushed"} {
		t.Run(failure, func(t *testing.T) {
			store := baseStore()
			store.repo.SourceType = models.SourceTypeGitHub
			job := twoFindingJob()
			job.ResumeAction = models.FixResumePush
			job.BaseBranch = "release"
			if failure == "already_pushed" {
				job.Pushed = true
				job.PushSHA = "published-sha"
			}
			ws := &stubWorkspace{path: t.TempDir(), branch: "wolf-fix/review"}
			ws.head = job.PushSHA
			job.WorkspacePath = ws.path
			summary := Summary{Branch: ws.branch, Kept: 2, Remaining: 1, Loops: 2, Rounds: []RoundSummary{{Round: 1, Kept: 2, Cleared: []string{"keep-me", "escalate-me"}}}, ReportMarkdown: "verification evidence"}
			data, _ := json.Marshal(summary)
			job.Summary = string(data)
			if failure == "bad_summary" {
				job.Summary = "invalid"
			}
			githubPR = func(_ context.Context, req pr.PRRequest) (*pr.PRResult, error) {
				if req.BaseBranch != "release" || req.Validation != "2 kept, 1 remaining" || len(req.Findings) != 2 {
					t.Fatalf("lost review evidence: %+v", req)
				}
				switch failure {
				case "error":
					return nil, errors.New("service unavailable")
				case "result_error":
					return &pr.PRResult{Error: "service unavailable"}, nil
				case "nil":
					return nil, nil
				case "empty":
					return &pr.PRResult{}, nil
				}
				return &pr.PRResult{URL: "https://github.com/example/repo/pull/1"}, nil
			}
			res, err := Run(t.Context(), job, Deps{Store: store, PublicationReady: func(context.Context, *models.Repo) (bool, string) { return true, "ready" }, Workspaces: &stubPreparer{ws: ws}, Engines: &chainSelector{}, Verifier: &stubVerifier{}, Diffs: &stubDiffs{}})
			if failure == "bad_summary" {
				if err == nil || ws.pushCalls != 0 || ws.cleanupCalls != 0 {
					t.Fatalf("invalid summary published/lost: %v %+v", err, ws)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.Summary.Kept != 2 || res.Summary.ReportMarkdown != summary.ReportMarkdown || len(store.attempts) != 0 {
				t.Fatalf("resume lost evidence or ran engines: %+v", res)
			}
			if failure == "" || failure == "already_pushed" {
				if ws.cleanupCalls != 1 || job.WorkspacePath != "" {
					t.Fatal("successful publication retained its completed workspace")
				}
				if failure == "already_pushed" && (ws.pushCalls != 0 || job.PushSHA != "published-sha") {
					t.Fatal("PR retry repushed or lost original SHA")
				}
				if !strings.Contains(job.Summary, `"pr_url":"https://github.com/example/repo/pull/1"`) || job.Status != models.FixJobSucceeded {
					t.Fatalf("missing durable PR result: %+v", job)
				}
			} else if job.Status != models.FixJobPushFailed || !job.Pushed || job.Error == "" || ws.cleanupCalls != 0 {
				t.Fatalf("PR failure hidden or branch lost: %+v %+v", job, ws)
			}
		})
	}
}

func TestPublicationReadinessPrecedesPush(t *testing.T) {
	original := githubPR
	t.Cleanup(func() { githubPR = original })
	githubPR = func(context.Context, pr.PRRequest) (*pr.PRResult, error) {
		t.Fatal("PR called before readiness")
		return nil, nil
	}
	for _, mode := range []string{models.FixModeDryRun, models.FixModePush, "resume", "missing-check"} {
		t.Run(mode, func(t *testing.T) {
			store := baseStore()
			store.repo.SourceType = models.SourceTypeGitHub
			job := twoFindingJob()
			job.Mode = mode
			if mode == "resume" {
				job.Mode = models.FixModeDryRun
				job.ResumeAction = models.FixResumePush
			}
			if mode == "missing-check" {
				job.Mode = models.FixModePush
			}
			ws := &stubWorkspace{path: t.TempDir(), branch: "wolf-fix/readiness"}
			cleanup := true
			checked := false
			deps := Deps{Store: store, Verifier: &stubVerifier{}, Diffs: &stubDiffs{}, PublicationReady: func(context.Context, *models.Repo) (bool, string) { checked = true; return false, "token cannot push" }}
			if mode == "missing-check" {
				deps.PublicationReady = nil
			}
			result, err := finishWithPush(t.Context(), job, store.repo, ws, deps, func(string, ...any) {}, Summary{Kept: 1, Addressed: []FindingNote{{ID: "keep-me"}}}, &cleanup)
			if err != nil {
				t.Fatal(err)
			}
			if ws.pushCalls != 0 || cleanup {
				t.Fatalf("unready/dry-run branch published or cleaned: %+v", ws)
			}
			if mode == models.FixModeDryRun {
				if checked || result.PauseStatus != models.FixJobAwaitingPush {
					t.Fatalf("dry run attempted publication: %+v", result)
				}
			} else if result.PauseStatus != models.FixJobPushFailed || result.Summary.PublicationError != "publication_not_ready" || job.Error == "" {
				t.Fatalf("readiness failure lost: %+v %+v", result, job)
			}
		})
	}
}

func TestPublicationRetainsFindingEvidenceAcrossReview(t *testing.T) {
	original := githubPR
	t.Cleanup(func() { githubPR = original })
	store := baseStore()
	store.repo.SourceType = models.SourceTypeGitHub
	finding := store.findings["keep-me"]
	finding.Title = "SQL injection"
	finding.Category = models.CategorySAST
	finding.LineStart = 42
	ws := &stubWorkspace{path: t.TempDir(), changed: []string{"main.go"}}
	job := twoFindingJob()
	job.MaxLoops = 1
	job.FindingIDList = []string{"keep-me"}
	deps := Deps{Store: store, Workspaces: &stubPreparer{ws: ws}, Diffs: &stubDiffs{}, PublicationReady: func(context.Context, *models.Repo) (bool, string) { return true, "ready" },
		Engines:  &chainSelector{build: func() EngineChain { return &stubChain{tiers: []engine.SubprocessEngine{&fixEngine{name: "test"}}} }},
		Verifier: &stubVerifier{pass: map[string]bool{"keep-me": true}, stages: []verify.StageResult{{Stage: verify.StageBuild, Passed: true, Detail: "password=do-not-persist"}, {Stage: verify.StageFindingCleared, Passed: true}, {Stage: verify.StageTests, Skipped: true}}},
	}
	result, err := Run(t.Context(), job, deps)
	if err != nil {
		t.Fatal(err)
	}
	if result.PauseStatus != models.FixJobAwaitingPush || len(result.Summary.Addressed) != 1 || strings.Contains(job.Summary, "do-not-persist") {
		t.Fatalf("missing/unsafe review evidence: %+v %s", result, job.Summary)
	}
	finding.Title = "changed after review"
	githubPR = func(_ context.Context, req pr.PRRequest) (*pr.PRResult, error) {
		if len(req.Findings) != 1 {
			t.Fatalf("findings: %+v", req.Findings)
		}
		f := req.Findings[0]
		if f.Title != "SQL injection" || f.ID != "keep-me" || f.ToolName != "gosec" || f.RuleID != "G1" || f.FilePath != "main.go" || f.LineStart != 42 || req.JobID != job.ID {
			t.Fatalf("lost review identity: %+v", req)
		}
		if req.Evidence[f.ID] != "build: passed; finding_cleared: passed; tests: skipped" {
			t.Fatalf("invented/lost checks: %+v", req.Evidence)
		}
		return &pr.PRResult{URL: "https://github.com/example/repo/pull/1"}, nil
	}
	job.ResumeAction = models.FixResumePush
	deps.Writability = failWritability{reason: "old generic check must not strand a publication retry"}
	if _, err := Run(t.Context(), job, deps); err != nil {
		t.Fatal(err)
	}
	if job.Status != models.FixJobSucceeded {
		t.Fatalf("publish failed: %+v", job)
	}
}

type finalWriteStore struct {
	*stubStore
	beforeFinal func(*models.FixJob) error
}

func (s *finalWriteStore) UpdateFixJob(ctx context.Context, job *models.FixJob) error {
	if job.Status == models.FixJobSucceeded && s.beforeFinal != nil {
		if err := s.beforeFinal(job); err != nil {
			return err
		}
	}
	return s.stubStore.UpdateFixJob(ctx, job)
}

func TestCleanupWaitsForDurableResult(t *testing.T) {
	for _, name := range []string{"saved", "write-failed", "stale-claim", "cleanup-failed"} {
		t.Run(name, func(t *testing.T) {
			ws := &stubWorkspace{path: t.TempDir(), changed: []string{"main.go"}}
			job := twoFindingJob()
			job.FindingIDList = []string{"keep-me"}
			failure := errors.New("result database unavailable")
			if name == "stale-claim" {
				failure = db.ErrFixJobConflict
			}
			if name == "cleanup-failed" {
				ws.cleanupErr = errors.New("worktree is locked")
			}
			writes := 0
			store := &finalWriteStore{stubStore: baseStore(), beforeFinal: func(*models.FixJob) error {
				writes++
				if writes == 1 && ws.cleanupCalls != 0 {
					t.Fatal("workspace deleted before final state commit")
				}
				if name == "write-failed" || name == "stale-claim" {
					return failure
				}
				return nil
			}}
			deps := Deps{Store: store, Workspaces: &stubPreparer{ws: ws}, Diffs: &stubDiffs{},
				Engines:  &chainSelector{build: func() EngineChain { return &stubChain{tiers: []engine.SubprocessEngine{&fixEngine{}}} }},
				Verifier: &stubVerifier{pass: map[string]bool{"keep-me": true}},
			}
			result, err := Run(t.Context(), job, deps)
			if name == "write-failed" || name == "stale-claim" {
				if !errors.Is(err, failure) || ws.cleanupCalls != 0 || result == nil || job.WorkspacePath != ws.path {
					t.Fatalf("unsaved work was not retained: result=%+v err=%v cleanup=%d job=%+v", result, err, ws.cleanupCalls, job)
				}
				if store.jobs[len(store.jobs)-1].Status != models.FixJobRunning {
					t.Fatal("failed final write reported a durable success")
				}
				return
			}
			if err != nil || ws.cleanupCalls != 1 {
				t.Fatalf("completion: cleanup=%d err=%v", ws.cleanupCalls, err)
			}
			saved := store.jobs[len(store.jobs)-1]
			if saved.Status != models.FixJobSucceeded {
				t.Fatalf("lost saved result: %+v", saved)
			}
			if name == "cleanup-failed" {
				if saved.WorkspacePath != ws.path || !strings.Contains(saved.Error, "cleanup failed") {
					t.Fatalf("lost cleanup recovery evidence: %+v", saved)
				}
			} else if saved.WorkspacePath != "" {
				t.Fatal("success left a dangling workspace path")
			}
		})
	}
}
