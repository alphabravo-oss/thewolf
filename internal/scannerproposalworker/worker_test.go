package scannerproposalworker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

const (
	testLock   = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testCommit = "0123456789abcdef0123456789abcdef01234567"
)

type proposerFunc func(context.Context, Request) (Result, error)

func (function proposerFunc) Propose(ctx context.Context, request Request) (Result, error) {
	return function(ctx, request)
}

func TestWorkerQueuesCompleteBuildPlanFromProposal(t *testing.T) {
	store, persistence, candidate := proposalFixture(t)
	_ = store
	proposer := proposerFunc(func(_ context.Context, request Request) (Result, error) {
		if request.CandidateID != candidate.ID ||
			request.DefinitionCommit != candidate.DefinitionCommit ||
			!json.Valid(request.Selection) || len(request.Updates) != 1 ||
			request.Updates[0].ComponentName != "semgrep" ||
			len(request.RequiredGates) == 0 || request.SourceDateEpoch <= 0 {
			t.Fatalf("proposal request = %#v", request)
		}
		return validResult(), nil
	})
	worker, err := New(Config{
		Store: persistence, Proposer: proposer, WorkerID: "proposal-worker", Once: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce processed=%t err=%v", processed, err)
	}
	got, err := persistence.GetCandidate(context.Background(), candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != scannerrelease.CandidateQueued ||
		got.ProposedCommit != testCommit ||
		got.LockDigest != testLock {
		t.Fatalf("queued proposal = %#v", got)
	}
	runs, err := persistence.ListBuildRuns(context.Background(), candidate.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("build runs=%#v err=%v", runs, err)
	}
	steps, err := persistence.ListBuildSteps(context.Background(), runs[0].ID)
	if err != nil || len(steps) < 20 {
		t.Fatalf("build steps=%d err=%v", len(steps), err)
	}
}

func TestScheduledCandidateWithIdenticalInputsCompletesAsAuditedNoOp(t *testing.T) {
	_, persistence, candidate := proposalFixtureWithUpdates(t, false)
	var proposerCalls atomic.Int64
	worker := newTestWorker(t, persistence, "proposal-worker", proposerFunc(func(
		context.Context,
		Request,
	) (Result, error) {
		proposerCalls.Add(1)
		return validResult(), nil
	}))
	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce processed=%t err=%v", processed, err)
	}
	if proposerCalls.Load() != 0 {
		t.Fatalf("proposer calls = %d, want 0", proposerCalls.Load())
	}
	got, err := persistence.GetCandidate(context.Background(), candidate.ID)
	if err != nil || got.State != scannerrelease.CandidateRejected ||
		got.ErrorClass != "no_changes" || got.ProposalCompletedAt == nil ||
		got.ProposalLeaseToken != "" {
		t.Fatalf("no-op candidate = %#v err=%v", got, err)
	}
	events, err := persistence.ListEvents(context.Background(), "candidate", candidate.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	foundNoOp := false
	for _, event := range events {
		if event.EventType == "candidate.noop" && strings.Contains(event.PayloadJSON, "no_changes") {
			foundNoOp = true
		}
	}
	if !foundNoOp {
		t.Fatalf("candidate events = %#v", events)
	}
	builds, err := persistence.ListBuildRuns(context.Background(), candidate.ID)
	if err != nil || len(builds) != 0 {
		t.Fatalf("no-op builds = %#v err=%v", builds, err)
	}
}

func TestConcurrentProposalWorkersCommitOnce(t *testing.T) {
	_, persistence, candidate := proposalFixture(t)
	arrived := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	var once sync.Once
	proposer := proposerFunc(func(context.Context, Request) (Result, error) {
		calls.Add(1)
		once.Do(func() { close(arrived) })
		<-release
		return validResult(), nil
	})
	workers := make([]*Worker, 2)
	for index := range workers {
		worker, err := New(Config{
			Store: persistence, Proposer: proposer,
			WorkerID: "proposal-worker-" + string(rune('a'+index)), Once: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		workers[index] = worker
	}
	var wait sync.WaitGroup
	type workerResult struct {
		processed bool
		err       error
	}
	results := make(chan workerResult, 2)
	for _, worker := range workers {
		wait.Add(1)
		go func(worker *Worker) {
			defer wait.Done()
			processed, err := worker.RunOnce(context.Background())
			results <- workerResult{processed: processed, err: err}
		}(worker)
	}
	<-arrived
	close(release)
	wait.Wait()
	close(results)
	processed := 0
	for result := range results {
		if result.err != nil && !errors.Is(result.err, ErrProposalRaceLost) {
			t.Fatal(result.err)
		}
		if result.processed {
			processed++
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("external proposer calls=%d want=1", calls.Load())
	}
	if processed < 1 {
		t.Fatalf("processed workers=%d want at least one", processed)
	}
	runs, err := persistence.ListBuildRuns(context.Background(), candidate.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("build runs=%#v err=%v", runs, err)
	}
}

func TestValidateResultRejectsMutableOrCredentialedReferences(t *testing.T) {
	for _, mutate := range []func(*Result){
		func(result *Result) { result.LockDigest = "latest" },
		func(result *Result) { result.LockURI = "https://user:secret@example.test/lock@" + testLock },
		func(result *Result) { result.LockURI = "http://example.test/lock@" + testLock },
		func(result *Result) { result.ProposalURL = "https://example.test/pr?token=secret" },
		func(result *Result) { result.ProposedCommit = strings.ToUpper(testCommit) },
	} {
		result := validResult()
		mutate(&result)
		if validateResult(result) == nil {
			t.Fatalf("unsafe proposal accepted: %#v", result)
		}
	}
}

func TestWorkerRedactsPersistedProposalErrors(t *testing.T) {
	_, persistence, candidate := proposalFixture(t)
	worker := newTestWorker(t, persistence, "proposal-worker", proposerFunc(func(
		context.Context,
		Request,
	) (Result, error) {
		return Result{}, errors.New(
			"token=topsecret password=hunter2 https://alice:credential@example.test/private",
		)
	}))
	processed, err := worker.RunOnce(context.Background())
	if !processed || err == nil {
		t.Fatalf("RunOnce processed=%t err=%v", processed, err)
	}
	for _, secret := range []string{"topsecret", "hunter2", "alice:credential"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("returned proposal error leaked %q: %v", secret, err)
		}
	}
	got, getErr := persistence.GetCandidate(context.Background(), candidate.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if got.State != scannerrelease.CandidateAwaitingDefinition ||
		got.ProposalErrorClass != "proposal_execution" ||
		got.ProposalLeaseToken != "" {
		t.Fatalf("released candidate = %#v", got)
	}
	for _, secret := range []string{"topsecret", "hunter2", "alice:credential"} {
		if strings.Contains(got.ProposalErrorDetail, secret) {
			t.Fatalf("proposal error leaked %q: %s", secret, got.ProposalErrorDetail)
		}
	}
	if !strings.Contains(got.ProposalErrorDetail, "[REDACTED]") {
		t.Fatalf("proposal error was not visibly redacted: %s", got.ProposalErrorDetail)
	}
}

func TestCommandProposerRedactsCredentialedStderr(t *testing.T) {
	proposer := CommandProposer{
		Path: "/bin/sh",
		Args: []string{
			"-c",
			"printf '%s' 'token=topsecret password=hunter2 https://alice:credential@example.test/private' >&2; exit 9",
		},
	}
	_, err := proposer.Propose(context.Background(), Request{CandidateID: "candidate"})
	if err == nil {
		t.Fatal("command proposer unexpectedly succeeded")
	}
	for _, secret := range []string{"topsecret", "hunter2", "alice:credential"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("stderr error leaked %q: %v", secret, err)
		}
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("stderr error was not visibly redacted: %v", err)
	}
}

func TestWorkerStopsWhenCandidateStateChanges(t *testing.T) {
	_, persistence, candidate := proposalFixture(t)
	started := make(chan struct{})
	proposer := proposerFunc(func(ctx context.Context, _ Request) (Result, error) {
		close(started)
		<-ctx.Done()
		return Result{}, ctx.Err()
	})
	worker := newTestWorker(t, persistence, "proposal-worker", proposer)
	done := make(chan error, 1)
	go func() {
		_, err := worker.RunOnce(context.Background())
		done <- err
	}()
	<-started
	claimed, err := persistence.GetCandidate(context.Background(), candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.TransitionCandidate(
		context.Background(), candidate.ID, claimed.Version,
		scannerrelease.CandidateRejected,
		scannerrelease.TransitionCommand{
			Actor: "operator", Reason: "cancel proposal",
			IdempotencyKey: "reject:" + candidate.ID, PayloadJSON: "{}",
		},
	); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrProposalRaceLost) {
			t.Fatalf("RunOnce error=%v want ErrProposalRaceLost", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proposal worker did not stop after candidate state change")
	}
	got, err := persistence.GetCandidate(context.Background(), candidate.ID)
	if err != nil || got.State != scannerrelease.CandidateRejected ||
		got.ProposedCommit != "" {
		t.Fatalf("rejected candidate = %#v err=%v", got, err)
	}
}

func TestWorkerRecoversStaleClaimAfterRestart(t *testing.T) {
	_, persistence, candidate := proposalFixture(t)
	now := time.Now().UTC()
	claimed, err := persistence.ClaimNextCandidateProposal(
		context.Background(), "dead-worker", now.Add(time.Second),
	)
	if err != nil || claimed == nil || claimed.ID != candidate.ID {
		t.Fatalf("initial claim=%#v err=%v", claimed, err)
	}
	var calls atomic.Int64
	worker, err := New(Config{
		Store: persistence,
		Proposer: proposerFunc(func(context.Context, Request) (Result, error) {
			calls.Add(1)
			return validResult(), nil
		}),
		WorkerID:          "replacement-worker",
		HeartbeatInterval: 5 * time.Millisecond,
		LeaseDuration:     50 * time.Millisecond,
		DrainTimeout:      time.Second,
		Now:               func() time.Time { return now.Add(2 * time.Second) },
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("replacement RunOnce processed=%t err=%v", processed, err)
	}
	got, err := persistence.GetCandidate(context.Background(), candidate.ID)
	if err != nil || got.State != scannerrelease.CandidateQueued ||
		got.ProposalAttempt != 2 || calls.Load() != 1 {
		t.Fatalf("recovered candidate=%#v calls=%d err=%v", got, calls.Load(), err)
	}
}

func proposalFixture(
	t *testing.T,
) (*db.SQLiteStore, scannerrelease.Persistence, *scannerrelease.Candidate) {
	return proposalFixtureWithUpdates(t, true)
}

func proposalFixtureWithUpdates(
	t *testing.T,
	withUpdates bool,
) (*db.SQLiteStore, scannerrelease.Persistence, *scannerrelease.Candidate) {
	t.Helper()
	store, err := db.NewSQLite(t.TempDir() + "/proposal.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	persistence := store.ScannerReleases()
	rules := scannerpolicy.Default()
	rulesJSON, _ := json.Marshal(rules)
	policy := &scannerrelease.Policy{
		ID: uuid.NewString(), Scope: "global", Revision: 1, Enabled: true,
		ScheduleJSON: "{}", RulesJSON: string(rulesJSON), CreatedBy: "test",
	}
	if err := persistence.CreatePolicy(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	discovery := &scannerrelease.DiscoveryRun{
		ID: uuid.NewString(), Trigger: scannerrelease.DiscoveryOnDemand,
		DefinitionCommit: testCommit, PolicyID: policy.ID, PolicyRevision: policy.Revision,
		ScopeJSON: `{ "mode": "complete" }`, State: scannerrelease.DiscoveryCompleted,
		Actor: "scheduler", IdempotencyKey: "discovery/" + uuid.NewString(),
	}
	if err := persistence.CreateDiscoveryRun(
		context.Background(), discovery,
		scannerrelease.TransitionCommand{
			Actor: "scheduler", Reason: "test", IdempotencyKey: discovery.IdempotencyKey,
			PayloadJSON: "{}",
		},
	); err != nil {
		t.Fatal(err)
	}
	if withUpdates {
		if err := persistence.AddUpdateItems(context.Background(), discovery.ID, []scannerrelease.UpdateItem{{
			ComponentType: scannerrelease.ComponentTool, ComponentName: "semgrep",
			CurrentValue: "1.0.0", AvailableValue: "1.0.1", Status: "update_available",
			RiskClass: scannerrelease.RiskLow, SelectionState: "unselected",
			SourceEvidenceJSON: `{}`, CompatibilityJSON: `{}`,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := persistence.ListUpdateItems(context.Background(), discovery.ID)
	wantItems := 0
	if withUpdates {
		wantItems = 1
	}
	if err != nil || len(items) != wantItems {
		t.Fatalf("discovery items=%#v err=%v", items, err)
	}
	gates, _ := json.Marshal(rules.RequiredGates)
	selectionJSON := `{"mode":"complete","no_op_if_unchanged":true}`
	if withUpdates {
		selectionJSON = `{"mode":"explicit","items":["` + items[0].ID + `"]}`
	}
	candidate := &scannerrelease.Candidate{
		ID: uuid.NewString(), DiscoveryRunID: discovery.ID, DefinitionCommit: testCommit,
		SelectionJSON:     selectionJSON,
		RiskSummaryJSON:   `{"highest_risk":"low"}`,
		State:             scannerrelease.CandidateAwaitingDefinition,
		RequiredGatesJSON: string(gates), PolicyID: policy.ID, PolicyRevision: policy.Revision,
		Actor: "scheduler", IdempotencyKey: "candidate/" + uuid.NewString(),
	}
	if err := persistence.CreateCandidate(
		context.Background(), candidate,
		scannerrelease.TransitionCommand{
			Actor: "scheduler", Reason: "test", IdempotencyKey: candidate.IdempotencyKey,
			PayloadJSON: "{}",
		},
	); err != nil {
		t.Fatal(err)
	}
	return store, persistence, candidate
}

func validResult() Result {
	return Result{
		ProposedCommit: testCommit,
		ProposalURL:    "https://github.example.test/acme/scanners/pull/42",
		LockDigest:     testLock,
		LockURI:        "oci://registry.example.test/locks@" + testLock,
		RiskSummary:    json.RawMessage(`{"highest_risk":"low"}`),
	}
}

func newTestWorker(
	t *testing.T,
	store scannerrelease.Persistence,
	workerID string,
	proposer Proposer,
) *Worker {
	t.Helper()
	worker, err := New(Config{
		Store: store, Proposer: proposer, WorkerID: workerID,
		HeartbeatInterval: 5 * time.Millisecond,
		LeaseDuration:     50 * time.Millisecond,
		DrainTimeout:      time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}
