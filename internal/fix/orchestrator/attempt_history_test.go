package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/fix/verify"
	"github.com/alphabravocompany/thewolf/internal/models"
)

type durableHistoryStore struct {
	*stubStore
	history db.Store
}

func (s durableHistoryStore) CreateFixAttempt(ctx context.Context, att *models.FixAttempt) error {
	return s.history.CreateFixAttempt(ctx, att)
}
func (s durableHistoryStore) UpdateFixAttempt(ctx context.Context, att *models.FixAttempt) error {
	return s.history.UpdateFixAttempt(ctx, att)
}
func (s durableHistoryStore) ListFixAttempts(ctx context.Context, id string) ([]models.FixAttempt, error) {
	return s.history.ListFixAttempts(ctx, id)
}

func TestAttemptHistorySurvivesCancellationCrashAndReopen(t *testing.T) {
	for _, mode := range []string{"cancelled", "user-cancelled", "crashed", "verified-before-summary"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "history.db")
			database, err := db.NewSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			job := twoFindingJob()
			job.FindingIDList, job.MaxLoops = []string{"keep-me"}, 1
			if err := database.EnqueueFixJob(t.Context(), job); err != nil {
				t.Fatal(err)
			}
			store := durableHistoryStore{baseStore(), database}
			ws := &stubWorkspace{path: t.TempDir(), branch: "wolf-fix/history", changed: []string{"main.go"}}
			job.WorkspacePath, job.TargetBranch = ws.path, ws.branch
			calls := 0
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			editor := turnEngine(func(engine.FixRequest) (*engine.FixResult, error) {
				calls++
				history, err := store.ListFixAttempts(t.Context(), job.ID)
				if err != nil || len(history) != 1 || history[0].Outcome != models.FixOutcomeRunning || history[0].ID == "" {
					t.Fatalf("engine started without durable history: %+v %v", history, err)
				}
				cancel()
				if mode == "user-cancelled" {
					current, _ := store.GetFixJobByID(t.Context(), job.ID)
					current.Status = models.FixJobCancelled
					if err := store.UpdateFixJob(t.Context(), current); err != nil {
						t.Fatal(err)
					}
				}
				return nil, context.Canceled
			})
			deps := Deps{Store: store, Workspaces: &stubPreparer{ws: ws}, Diffs: &stubDiffs{}, Verifier: &stubVerifier{pass: map[string]bool{"keep-me": true}},
				Engines: &chainSelector{build: func() EngineChain { return &stubChain{tiers: []engine.SubprocessEngine{editor}} }},
			}
			if mode == "cancelled" || mode == "user-cancelled" {
				if _, err := Run(ctx, job, deps); !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled engine returned success: %v", err)
				}
				if calls != 1 || ws.commitCalls != 0 || ws.pushCalls != 0 || ws.cleanupCalls != 0 {
					t.Fatalf("cancelled attempt continued: %+v", ws)
				}
			} else {
				outcome := models.FixOutcomeRunning
				if mode == "verified-before-summary" {
					outcome = models.FixOutcomeKept
				}
				if err := database.CreateFixAttempt(t.Context(), &models.FixAttempt{ID: "previous", JobID: job.ID, FindingID: "keep-me", AttemptNo: 1, Outcome: outcome, CreatedAt: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
				job.CurrentLoop = 1 // worker stopped before the round-completion write
			}
			before, err := database.ListFixAttempts(t.Context(), job.ID)
			if err != nil || len(before) != 1 {
				t.Fatalf("lost first attempt: %v %v", before, err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if mode == "user-cancelled" {
				if job.Status != models.FixJobCancelled || before[0].Outcome != models.FixOutcomeInterrupted {
					t.Fatalf("cancelled job/history was overwritten: %+v %+v", job, before)
				}
				return
			}
			database, err = db.NewSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			store.history, deps.Store = database, durableHistoryStore{store.stubStore, database}
			deps.Engines = &chainSelector{build: func() EngineChain {
				return &stubChain{tiers: []engine.SubprocessEngine{turnEngine(func(engine.FixRequest) (*engine.FixResult, error) {
					calls++
					return &engine.FixResult{EditsInPlace: true}, nil
				})}}
			}}
			if mode == "verified-before-summary" {
				deps.Rescan = remainingRescanner{}
			}
			job.Status = models.FixJobQueued
			result, err := Run(t.Context(), job, deps)
			if err != nil || result.Summary.Kept != 1 || job.Error != "" {
				t.Fatalf("restart skipped or lost the repair: %+v %v", result, err)
			}
			history, err := database.ListFixAttempts(t.Context(), job.ID)
			if err != nil || len(history) == 0 {
				t.Fatalf("missing reopened history: %v", err)
			}
			want := 2
			if mode == "verified-before-summary" {
				want = 1
				if calls != 0 {
					t.Fatal("repeated agent work despite a verified journal and clean rescan")
				}
			} else if len(history) != 2 || history[0].Outcome != models.FixOutcomeInterrupted || history[1].AttemptNo != 2 {
				t.Fatalf("restart replaced/reset interrupted history: %+v", history)
			}
			if err != nil || len(history) != want || history[0].ID != before[0].ID || history[len(history)-1].Outcome != models.FixOutcomeKept {
				t.Fatalf("history changed across reopen: %+v %v", history, err)
			}
			returned, _ := json.Marshal(result.Attempts)
			saved, _ := json.Marshal(history)
			if string(returned) != string(saved) {
				t.Fatal("returned attempts differ from durable history")
			}
		})
	}
}

type cancellingHistoryVerifier struct {
	stubVerifier
	cancel context.CancelFunc
	final  bool
}

func (v *cancellingHistoryVerifier) VerifyBatch(ctx context.Context, ws verify.Workspace, findings []models.Finding) (map[string]*verify.VerifyResult, error) {
	if !v.final {
		v.cancel()
		return nil, ctx.Err()
	}
	return v.stubVerifier.VerifyBatch(ctx, ws, findings)
}

func (v *cancellingHistoryVerifier) VerifyBranch(ctx context.Context, _ verify.Workspace, _, _ []models.Finding, _ []string) ([]verify.StageResult, error) {
	v.cancel()
	return nil, ctx.Err()
}

func TestCancellationDuringVerificationRetainsTerminalHistory(t *testing.T) {
	for _, final := range []bool{false, true} {
		t.Run(map[bool]string{false: "turn-verification", true: "final-verification"}[final], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			store := baseStore()
			job := twoFindingJob()
			job.FindingIDList, job.Mode = []string{"keep-me"}, models.FixModePush
			ws := &stubWorkspace{path: t.TempDir(), changed: []string{"main.go"}}
			v := &cancellingHistoryVerifier{stubVerifier: stubVerifier{pass: map[string]bool{"keep-me": true}}, cancel: cancel, final: final}
			_, err := Run(ctx, job, Deps{Store: store, Workspaces: &stubPreparer{ws: ws}, Verifier: v, Diffs: &stubDiffs{},
				Engines: &chainSelector{build: func() EngineChain { return &stubChain{tiers: []engine.SubprocessEngine{&fixEngine{}}} }},
			})
			if err == nil || job.Status != models.FixJobFailed || ws.pushCalls != 0 || ws.cleanupCalls != 0 || job.WorkspacePath != ws.path {
				t.Fatalf("cancelled verification accepted or lost workspace: %v %+v", err, job)
			}
			if len(store.attempts) != 1 || store.attempts[0].ID == "" || store.attempts[0].Outcome == models.FixOutcomeRunning || (!final && store.attempts[0].Outcome == models.FixOutcomeKept) {
				t.Fatalf("cancelled verification left false/pending history: %+v", store.attempts)
			}
		})
	}
}

type failedHistoryStore struct {
	*stubStore
	mode  string
	reads int
}

func (s *failedHistoryStore) ListFixAttempts(ctx context.Context, job string) ([]models.FixAttempt, error) {
	s.reads++
	if s.mode == "restore" || (s.mode == "final-read" && s.reads == 2) {
		return nil, errors.New("history read failed")
	}
	return s.stubStore.ListFixAttempts(ctx, job)
}

func (s *failedHistoryStore) CreateFixAttempt(ctx context.Context, att *models.FixAttempt) error {
	if s.mode == "start" {
		return errors.New("history start failed")
	}
	return s.stubStore.CreateFixAttempt(ctx, att)
}

func TestHistoryFailuresStopBeforeExecutionOrPublication(t *testing.T) {
	for _, mode := range []string{"start", "restore", "final-read"} {
		t.Run(mode, func(t *testing.T) {
			store := &failedHistoryStore{stubStore: baseStore(), mode: mode}
			ws := &stubWorkspace{path: t.TempDir(), changed: []string{"main.go"}}
			job := twoFindingJob()
			job.Mode, job.FindingIDList = models.FixModePush, []string{"keep-me"}
			calls := 0
			editor := turnEngine(func(engine.FixRequest) (*engine.FixResult, error) {
				calls++
				return &engine.FixResult{EditsInPlace: true}, nil
			})
			_, err := Run(t.Context(), job, Deps{Store: store, Workspaces: &stubPreparer{ws: ws}, Diffs: &stubDiffs{}, Verifier: &stubVerifier{pass: map[string]bool{"keep-me": true}},
				Engines: &chainSelector{build: func() EngineChain { return &stubChain{tiers: []engine.SubprocessEngine{editor}} }},
			})
			if err == nil || ws.pushCalls != 0 || ws.cleanupCalls != 0 || job.Status != models.FixJobFailed {
				t.Fatalf("history failure was ignored: %+v %v", ws, err)
			}
			if mode != "final-read" && calls != 0 {
				t.Fatal("engine ran without readable/persisted history")
			}
		})
	}
}
