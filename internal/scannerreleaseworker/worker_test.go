package scannerreleaseworker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
)

const (
	testLockDigest     = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testOutputDigest   = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testDecisionDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func TestWorkerExecutesDAGRetriesAndRedactsEvidence(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t, successPlan())
	executor := &recordingExecutor{failOnce: map[string]bool{"lock-reproducibility": true}}
	worker := newWorker(t, fixture.persistence, executor, scannerreleaseworker.Config{
		MaxParallelSteps: 2,
		MaxStepAttempts:  2,
		WorkspaceRoot:    t.TempDir(),
	})

	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	build, err := fixture.persistence.GetBuildRun(context.Background(), fixture.build.ID)
	if err != nil {
		t.Fatal(err)
	}
	if build.State != scannerrelease.BuildCompleted {
		t.Fatalf("build state = %s", build.State)
	}
	candidate, err := fixture.persistence.GetCandidate(context.Background(), fixture.candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.State != scannerrelease.CandidateAwaitingApproval ||
		candidate.PolicyDecision == testDecisionDigest ||
		!strings.HasPrefix(candidate.PolicyDecision, "sha256:") {
		t.Fatalf("candidate after build = %#v", candidate)
	}
	steps, err := fixture.persistence.ListBuildSteps(context.Background(), fixture.build.ID)
	if err != nil {
		t.Fatal(err)
	}
	lockAttempts := 0
	for _, step := range steps {
		if strings.Contains(step.SummaryJSON, "super-secret") ||
			strings.Contains(step.SummaryJSON, "Bearer executor-token") {
			t.Fatalf("unredacted evidence in %s: %s", step.StepKey, step.SummaryJSON)
		}
		if step.StepKey == "lock-reproducibility" {
			lockAttempts++
		}
	}
	if lockAttempts != 2 {
		t.Fatalf("lock attempts = %d, want 2", lockAttempts)
	}
	if executor.maxActive.Load() < 2 {
		t.Fatalf("max parallel executor calls = %d, want at least 2", executor.maxActive.Load())
	}
	if executor.callCount("checkout") != 1 || executor.callCount("lock-reproducibility") != 2 {
		t.Fatalf("executor calls = %#v", executor.callsSnapshot())
	}
	policyRequest := executor.lastRequest("policy-evaluation")
	for _, dependency := range []string{"test-a", "test-b"} {
		evidence, ok := policyRequest.Dependencies[dependency]
		if !ok || evidence.OutputDigest != testOutputDigest {
			t.Fatalf("policy dependency %q = %#v", dependency, evidence)
		}
	}
	for _, workspace := range executor.workspacesSnapshot() {
		if _, err := os.Stat(workspace); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("ephemeral workspace %q still exists, stat err=%v", workspace, err)
		}
	}
}

func TestWorkerAppliesTrustedAutomaticApproval(t *testing.T) {
	t.Parallel()
	rules := scannerpolicy.Default()
	rules.ApprovalMode = scannerpolicy.ApprovalPolicyGated
	fixture := newFixtureWithPolicy(t, successPlan(), rules)
	worker := newWorker(t, fixture.persistence, &recordingExecutor{}, scannerreleaseworker.Config{})
	if processed, err := worker.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	candidate, err := fixture.persistence.GetCandidate(context.Background(), fixture.candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.State != scannerrelease.CandidateApproved {
		t.Fatalf("auto-approved candidate state = %s", candidate.State)
	}
	approvals, err := fixture.persistence.ListApprovals(context.Background(), candidate.ID, "")
	if err != nil || len(approvals) != 1 {
		t.Fatalf("automatic approvals=%#v err=%v", approvals, err)
	}
	if approvals[0].Actor == candidate.Actor ||
		approvals[0].PolicyDecision != candidate.PolicyDecision {
		t.Fatalf("invalid automatic approval: %#v candidate=%#v", approvals[0], candidate)
	}
}

func TestWorkerComputesClosedMaintenanceWindowFromPersistedPolicy(t *testing.T) {
	t.Parallel()
	rules := scannerpolicy.Default()
	rules.ApprovalMode = scannerpolicy.ApprovalPolicyGated
	schedule := scannerpolicy.DefaultSchedule()
	schedule.MaintenanceWindow = []scannerpolicy.MaintenanceWindow{{
		ID: "sunday", Name: "Sunday", Cron: "0 3 * * 0", Duration: "1h",
	}}
	fixture := newFixtureWithPolicyAndSchedule(t, successPlan(), rules, schedule)
	worker := newWorker(t, fixture.persistence, &recordingExecutor{}, scannerreleaseworker.Config{
		Now: func() time.Time { return time.Date(2099, 7, 30, 21, 0, 0, 0, time.UTC) },
	})
	if processed, err := worker.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	candidate, err := fixture.persistence.GetCandidate(context.Background(), fixture.candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.State != scannerrelease.CandidateAwaitingApproval {
		t.Fatalf("candidate state outside maintenance window = %s", candidate.State)
	}
	approvals, err := fixture.persistence.ListApprovals(context.Background(), candidate.ID, "")
	if err != nil || len(approvals) != 0 {
		t.Fatalf("closed-window approvals=%#v err=%v", approvals, err)
	}
}

func TestWorkerPersistsPolicyBlockWithoutApproval(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t, successPlan())
	input := passingPolicyInput()
	input.Gates[0].Status = scannerpolicy.GateFailed
	worker := newWorker(
		t,
		fixture.persistence,
		&recordingExecutor{policyInput: input},
		scannerreleaseworker.Config{},
	)
	if processed, err := worker.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	candidate, err := fixture.persistence.GetCandidate(context.Background(), fixture.candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.State != scannerrelease.CandidateBlocked ||
		candidate.ErrorClass != "policy_blocked" ||
		!strings.Contains(candidate.ErrorDetail, "required gate") {
		t.Fatalf("policy-blocked candidate = %#v", candidate)
	}
	approvals, err := fixture.persistence.ListApprovals(context.Background(), candidate.ID, "")
	if err != nil || len(approvals) != 0 {
		t.Fatalf("blocked candidate approvals=%#v err=%v", approvals, err)
	}
}

func TestWorkerCooperativelyCancelsRunningStep(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t, scannerpipeline.Plan{Steps: []scannerpipeline.Step{
		{Key: "checkout", Kind: scannerpipeline.StepCheckout, Timeout: 5 * time.Second, Required: true},
	}})
	started := make(chan struct{})
	executor := executorFunc(func(ctx context.Context, _ scannerreleaseworker.StepRequest) (scannerreleaseworker.StepResult, error) {
		close(started)
		<-ctx.Done()
		return scannerreleaseworker.StepResult{}, ctx.Err()
	})
	worker := newWorker(t, fixture.persistence, executor, scannerreleaseworker.Config{
		HeartbeatInterval: 10 * time.Millisecond,
		LeaseDuration:     100 * time.Millisecond,
		DrainTimeout:      time.Second,
	})
	result := make(chan error, 1)
	go func() {
		_, err := worker.RunOnce(context.Background())
		result <- err
	}()
	<-started
	_, err := fixture.persistence.RequestBuildCancellation(
		context.Background(), fixture.build.ID,
		scannerrelease.TransitionCommand{
			Actor: "operator", Reason: "test cancellation", IdempotencyKey: "cancel/test",
			PayloadJSON: "{}",
		},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("worker cancellation error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not cooperatively cancel")
	}
	build, err := fixture.persistence.GetBuildRun(context.Background(), fixture.build.ID)
	if err != nil {
		t.Fatal(err)
	}
	if build.State != scannerrelease.BuildCancelled {
		t.Fatalf("cancelled build state = %s", build.State)
	}
	steps, _ := fixture.persistence.ListBuildSteps(context.Background(), fixture.build.ID)
	if steps[len(steps)-1].State != scannerrelease.BuildCancelled {
		t.Fatalf("cancelled step state = %s", steps[len(steps)-1].State)
	}
	candidate, err := fixture.persistence.GetCandidate(context.Background(), fixture.candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.State != scannerrelease.CandidateBlocked ||
		candidate.ErrorClass != "build_cancelled" {
		t.Fatalf("cancelled candidate = %#v", candidate)
	}
}

func TestWorkerStopsPublishingAfterLeaseLoss(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t, scannerpipeline.Plan{Steps: []scannerpipeline.Step{
		{Key: "checkout", Kind: scannerpipeline.StepCheckout, Timeout: time.Second, Required: true},
	}})
	store := &leaseLosingStore{Persistence: fixture.persistence, loseAt: 4}
	executor := executorFunc(func(
		_ context.Context,
		request scannerreleaseworker.StepRequest,
	) (scannerreleaseworker.StepResult, error) {
		return scannerreleaseworker.StepResult{
			OutputDigest: testOutputDigest,
			Verification: scannerreleaseworker.Verification{
				DefinitionCommit: request.DefinitionCommit,
				LockDigest:       request.LockDigest,
			},
		}, nil
	})
	worker := newWorker(t, store, executor, scannerreleaseworker.Config{
		HeartbeatInterval: time.Second,
		LeaseDuration:     3 * time.Second,
	})
	processed, err := worker.RunOnce(context.Background())
	if !processed || !errors.Is(err, scannerreleaseworker.ErrLeaseLost) {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	build, _ := fixture.persistence.GetBuildRun(context.Background(), fixture.build.ID)
	if build.State != scannerrelease.BuildRunning {
		t.Fatalf("lease-lost worker published build state %s", build.State)
	}
	steps, _ := fixture.persistence.ListBuildSteps(context.Background(), fixture.build.ID)
	if steps[0].State != scannerrelease.BuildRunning || steps[0].OutputDigest != "" {
		t.Fatalf("lease-lost worker published step result: %#v", steps[0])
	}
}

func TestReplacementWorkerReconcilesSameWorkspaceAndLogicalOperation(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t, successPlan())
	workspaceRoot := t.TempDir()
	externalCheckoutCalls := &atomic.Int64{}
	firstExecutor := &durableReplayExecutor{
		checkoutCalls: externalCheckoutCalls,
		delegate:      &recordingExecutor{},
	}
	firstWorker := newWorker(
		t,
		&leaseLosingStore{Persistence: fixture.persistence, loseAt: 4},
		firstExecutor,
		scannerreleaseworker.Config{
			WorkspaceRoot: workspaceRoot, MaxParallelSteps: 2, MaxStepAttempts: 2,
			HeartbeatInterval: time.Second, LeaseDuration: 3 * time.Second,
		},
	)
	processed, err := firstWorker.RunOnce(context.Background())
	if !processed || !errors.Is(err, scannerreleaseworker.ErrLeaseLost) {
		t.Fatalf("first worker processed=%v err=%v", processed, err)
	}
	firstRequest := firstExecutor.checkoutRequest(t)
	if firstRequest.LogicalOperationID == "" || firstRequest.StepAttempt != 1 {
		t.Fatalf("first logical request = %#v", firstRequest)
	}
	if _, err := os.Stat(firstRequest.Workspace); err != nil {
		t.Fatalf("lease-lost workspace was not retained: %v", err)
	}
	build, err := fixture.persistence.GetBuildRun(context.Background(), fixture.build.ID)
	if err != nil || build.State != scannerrelease.BuildRunning {
		t.Fatalf("lease-lost build = %#v err=%v", build, err)
	}
	steps, err := fixture.persistence.ListBuildSteps(context.Background(), fixture.build.ID)
	if err != nil || steps[0].State != scannerrelease.BuildRunning {
		t.Fatalf("lease-lost steps = %#v err=%v", steps, err)
	}
	if reclaimed, err := fixture.persistence.ReclaimStaleBuildRuns(
		context.Background(), time.Now().UTC().Add(10*time.Second),
	); err != nil || reclaimed != 1 {
		t.Fatalf("reclaim stale build count=%d err=%v", reclaimed, err)
	}
	requeued, err := fixture.persistence.GetBuildRun(context.Background(), fixture.build.ID)
	if err != nil || requeued.State != scannerrelease.BuildQueued ||
		requeued.ErrorClass != "" || requeued.CompletedAt != nil {
		t.Fatalf("requeued stale running build = %#v err=%v", requeued, err)
	}

	replacementExecutor := &durableReplayExecutor{
		checkoutCalls: externalCheckoutCalls,
		delegate:      &recordingExecutor{},
	}
	replacement := newWorker(t, fixture.persistence, replacementExecutor, scannerreleaseworker.Config{
		WorkspaceRoot: workspaceRoot, MaxParallelSteps: 2, MaxStepAttempts: 2,
	})
	processed, err = replacement.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("replacement worker processed=%v err=%v", processed, err)
	}
	replayed := replacementExecutor.checkoutRequest(t)
	if replayed.Workspace != firstRequest.Workspace ||
		replayed.LogicalOperationID != firstRequest.LogicalOperationID ||
		replayed.StepAttempt != firstRequest.StepAttempt {
		t.Fatalf("replacement request=%#v first=%#v", replayed, firstRequest)
	}
	if externalCheckoutCalls.Load() != 1 {
		t.Fatalf("checkout external operation executed %d times", externalCheckoutCalls.Load())
	}
	steps, err = fixture.persistence.ListBuildSteps(context.Background(), fixture.build.ID)
	if err != nil {
		t.Fatal(err)
	}
	checkoutAttempts := 0
	for _, step := range steps {
		if step.StepKey != "checkout" {
			continue
		}
		checkoutAttempts++
		if step.State != scannerrelease.BuildCompleted || step.Attempt != 1 ||
			!strings.Contains(step.SummaryJSON, "reconciled_after_worker_loss") ||
			!strings.Contains(step.SummaryJSON, firstRequest.LogicalOperationID) {
			t.Fatalf("reconciled checkout = %#v", step)
		}
	}
	if checkoutAttempts != 1 {
		t.Fatalf("checkout diagnostic attempts = %d", checkoutAttempts)
	}
	if _, err := os.Stat(firstRequest.Workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("terminal workspace still exists: %v", err)
	}
}

func TestWorkerDoesNotRetryReconciliationRequiredOperation(t *testing.T) {
	t.Parallel()
	plan := scannerpipeline.Plan{Steps: []scannerpipeline.Step{{
		Key: "candidate-publish/default", Kind: scannerpipeline.StepPublish,
		Timeout: time.Second, Retryable: true, Required: true,
	}}}
	fixture := newFixture(t, plan)
	workspaceRoot := t.TempDir()
	var calls atomic.Int64
	executor := executorFunc(func(
		_ context.Context,
		request scannerreleaseworker.StepRequest,
	) (scannerreleaseworker.StepResult, error) {
		calls.Add(1)
		return scannerreleaseworker.StepResult{}, fmt.Errorf(
			"provider acknowledgement missing: %w",
			scannerreleaseworker.ErrReconciliationRequired,
		)
	})
	worker := newWorker(t, fixture.persistence, executor, scannerreleaseworker.Config{
		WorkspaceRoot: workspaceRoot, MaxStepAttempts: 3,
	})
	processed, err := worker.RunOnce(context.Background())
	if !processed || !errors.Is(err, scannerreleaseworker.ErrReconciliationRequired) {
		t.Fatalf("reconciliation-required processed=%v err=%v", processed, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("ambiguous external operation executed %d times", calls.Load())
	}
	steps, listErr := fixture.persistence.ListBuildSteps(context.Background(), fixture.build.ID)
	if listErr != nil || len(steps) != 1 || steps[0].Attempt != 1 ||
		steps[0].State != scannerrelease.BuildFailed ||
		steps[0].ErrorClass != "reconciliation_required" ||
		!strings.Contains(steps[0].SummaryJSON, "logical_operation_id") {
		t.Fatalf("reconciliation-required steps=%#v err=%v", steps, listErr)
	}
	candidate, candidateErr := fixture.persistence.GetCandidate(
		context.Background(), fixture.candidate.ID,
	)
	if candidateErr != nil || candidate.State != scannerrelease.CandidateFailed ||
		candidate.ErrorClass != "reconciliation_required" {
		t.Fatalf("reconciliation-required candidate=%#v err=%v", candidate, candidateErr)
	}
	entries, readErr := os.ReadDir(workspaceRoot)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("terminal reconciliation workspace was not cleaned: entries=%v err=%v", entries, readErr)
	}
}

func TestWorkerSkipsPreviouslyCompletedSteps(t *testing.T) {
	t.Parallel()
	plan := scannerpipeline.Plan{Steps: []scannerpipeline.Step{
		{Key: "checkout", Kind: scannerpipeline.StepCheckout, Timeout: time.Second, Required: true},
		{
			Key: "policy-evaluation", Kind: scannerpipeline.StepPolicy,
			DependsOn: []string{"checkout"}, Timeout: time.Second, Required: true,
		},
	}}
	fixture := newFixture(t, plan)
	steps, _ := fixture.persistence.ListBuildSteps(context.Background(), fixture.build.ID)
	checkout := steps[0]
	var checkoutSummary map[string]any
	if err := json.Unmarshal([]byte(checkout.SummaryJSON), &checkoutSummary); err != nil {
		t.Fatal(err)
	}
	checkoutSummary["evidence"] = map[string]any{
		"summary": map[string]any{"status": "passed"},
		"verification": scannerreleaseworker.Verification{
			DefinitionCommit: scannerrelease.EffectiveDefinitionCommit(fixture.candidate),
			LockDigest:       fixture.candidate.LockDigest, PolicyID: fixture.policy.ID,
			PolicyRevision: fixture.policy.Revision,
		},
	}
	encodedCheckout, err := json.Marshal(checkoutSummary)
	if err != nil {
		t.Fatal(err)
	}
	checkout.OutputDigest = testOutputDigest
	checkout.SummaryJSON = string(encodedCheckout)
	persistedCheckout, err := fixture.persistence.UpdateBuildStepEvidence(
		context.Background(), &checkout, checkout.Version, command("seed-checkout-evidence"),
	)
	if err != nil {
		t.Fatal(err)
	}
	running, err := fixture.persistence.TransitionBuildStep(
		context.Background(), persistedCheckout.ID, persistedCheckout.Version, scannerrelease.BuildRunning,
		command("seed-checkout-running"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.persistence.TransitionBuildStep(
		context.Background(), running.ID, running.Version, scannerrelease.BuildCompleted,
		command("seed-checkout-completed"),
	); err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{}
	worker := newWorker(t, fixture.persistence, executor, scannerreleaseworker.Config{})
	if processed, err := worker.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	if executor.callCount("checkout") != 0 || executor.callCount("policy-evaluation") != 1 {
		t.Fatalf("resume executor calls = %#v", executor.callsSnapshot())
	}
}

func TestWorkerClaimsOnlyCompatiblePlatformMatrices(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t, scannerpipeline.Plan{Steps: []scannerpipeline.Step{
		{Key: "checkout", Kind: scannerpipeline.StepCheckout, Timeout: time.Second, Required: true},
	}})
	worker, err := scannerreleaseworker.New(scannerreleaseworker.Config{
		Store: fixture.persistence, Executor: &recordingExecutor{},
		WorkerID: "arm-only-worker", SupportedPlatforms: []string{"linux/arm64"},
		HeartbeatInterval: time.Second, LeaseDuration: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.RunOnce(context.Background())
	if err != nil || processed {
		t.Fatalf("incompatible claim processed=%v err=%v", processed, err)
	}
	build, err := fixture.persistence.GetBuildRun(context.Background(), fixture.build.ID)
	if err != nil {
		t.Fatal(err)
	}
	if build.State != scannerrelease.BuildQueued || build.LeaseToken != "" {
		t.Fatalf("incompatible build was claimed: %#v", build)
	}
}

func TestNewRejectsUnsafeLeaseConfiguration(t *testing.T) {
	t.Parallel()
	_, err := scannerreleaseworker.New(scannerreleaseworker.Config{
		Store: fixturePersistenceStub{}, Executor: executorFunc(nil), WorkerID: "worker",
		HeartbeatInterval: time.Second, LeaseDuration: 2 * time.Second,
	})
	if err == nil {
		t.Fatal("unsafe lease configuration accepted")
	}
}

type fixture struct {
	store       db.Store
	persistence scannerrelease.Persistence
	policy      *scannerrelease.Policy
	candidate   *scannerrelease.Candidate
	build       *scannerrelease.BuildRun
}

func newFixture(t *testing.T, plan scannerpipeline.Plan) fixture {
	return newFixtureWithPolicy(t, plan, scannerpolicy.Default())
}

func newFixtureWithPolicy(
	t *testing.T,
	plan scannerpipeline.Plan,
	rules scannerpolicy.Policy,
) fixture {
	return newFixtureWithPolicyAndSchedule(t, plan, rules, scannerpolicy.DefaultSchedule())
}

func newFixtureWithPolicyAndSchedule(
	t *testing.T,
	plan scannerpipeline.Plan,
	rules scannerpolicy.Policy,
	schedule scannerpolicy.SchedulePolicy,
) fixture {
	t.Helper()
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	store, err := db.NewSQLite(t.TempDir() + "/worker.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	persistence := store.ScannerReleases()
	rules.Revision = 1
	rulesJSON, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	scheduleJSON, err := json.Marshal(schedule)
	if err != nil {
		t.Fatal(err)
	}
	policy := &scannerrelease.Policy{
		ID: uuid.NewString(), Scope: "global", Revision: 1, Enabled: true,
		ScheduleJSON: string(scheduleJSON), RulesJSON: string(rulesJSON), CreatedBy: "test",
	}
	if err := persistence.CreatePolicy(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	candidate := &scannerrelease.Candidate{
		ID: uuid.NewString(), DefinitionCommit: "0123456789abcdef",
		ProposedCommit:    "fedcba9876543210",
		LockDigest:        testLockDigest,
		RiskSummaryJSON:   `{"highest_risk":"low","changes":[{"component":"semgrep","kind":"patch","from":"1.0.0","to":"1.0.1"}]}`,
		State:             scannerrelease.CandidateQueued,
		RequiredGatesJSON: mustJSON(rules.RequiredGates),
		PolicyID:          policy.ID, PolicyRevision: policy.Revision,
		Actor: "test", IdempotencyKey: uuid.NewString(),
	}
	if err := persistence.CreateCandidate(context.Background(), candidate, command("candidate")); err != nil {
		t.Fatal(err)
	}
	build := &scannerrelease.BuildRun{
		ID: uuid.NewString(), CandidateID: candidate.ID, Attempt: 1,
		State: scannerrelease.BuildQueued, PlatformsJSON: `["linux/amd64"]`,
	}
	if err := persistence.CreateBuildRun(context.Background(), build, command("build")); err != nil {
		t.Fatal(err)
	}
	for ordinal, step := range plan.Steps {
		summary, _ := json.Marshal(map[string]any{
			"kind": step.Kind, "depends_on": step.DependsOn,
			"timeout": step.Timeout.String(), "retryable": step.Retryable,
			"concurrency_key": step.ConcurrencyKey, "ordinal": ordinal,
		})
		record := &scannerrelease.BuildStep{
			ID: uuid.NewString(), BuildRunID: build.ID, StepKey: step.Key,
			State: scannerrelease.BuildQueued, Attempt: 1, SummaryJSON: string(summary),
			RetentionClass: "candidate-evidence",
		}
		if err := persistence.CreateBuildStep(
			context.Background(), record, command("step/"+step.Key),
		); err != nil {
			t.Fatal(err)
		}
	}
	return fixture{
		store: store, persistence: persistence, policy: policy, candidate: candidate, build: build,
	}
}

func successPlan() scannerpipeline.Plan {
	return scannerpipeline.Plan{Steps: []scannerpipeline.Step{
		{Key: "checkout", Kind: scannerpipeline.StepCheckout, Timeout: time.Second, Required: true},
		{
			Key: "lock-reproducibility", Kind: scannerpipeline.StepValidation,
			DependsOn: []string{"checkout"}, Timeout: time.Second, Retryable: true, Required: true,
		},
		{
			Key: "test-a", Kind: scannerpipeline.StepTest,
			DependsOn: []string{"lock-reproducibility"}, Timeout: time.Second, Required: true,
		},
		{
			Key: "test-b", Kind: scannerpipeline.StepTest,
			DependsOn: []string{"lock-reproducibility"}, Timeout: time.Second, Required: true,
		},
		{
			Key: "policy-evaluation", Kind: scannerpipeline.StepPolicy,
			DependsOn: []string{"test-a", "test-b"}, Timeout: time.Second, Required: true,
		},
		{
			Key: "candidate-evidence-summary", Kind: scannerpipeline.StepEvidence,
			DependsOn: []string{"policy-evaluation"}, Timeout: time.Second, Required: true,
		},
	}}
}

func newWorker(
	t *testing.T,
	store scannerrelease.Persistence,
	executor scannerreleaseworker.Executor,
	overrides scannerreleaseworker.Config,
) *scannerreleaseworker.Worker {
	t.Helper()
	overrides.Store = store
	overrides.Executor = executor
	overrides.WorkerID = "release-worker-" + uuid.NewString()
	overrides.SupportedPlatforms = []string{"linux/amd64"}
	if overrides.HeartbeatInterval == 0 {
		overrides.HeartbeatInterval = time.Second
	}
	if overrides.LeaseDuration == 0 {
		overrides.LeaseDuration = 3 * time.Second
	}
	worker, err := scannerreleaseworker.New(overrides)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

type executorFunc func(
	context.Context,
	scannerreleaseworker.StepRequest,
) (scannerreleaseworker.StepResult, error)

func (f executorFunc) Execute(
	ctx context.Context,
	request scannerreleaseworker.StepRequest,
) (scannerreleaseworker.StepResult, error) {
	if f == nil {
		return scannerreleaseworker.StepResult{}, nil
	}
	return f(ctx, request)
}

type recordingExecutor struct {
	mu          sync.Mutex
	calls       map[string]int
	workspaces  []string
	requests    map[string][]scannerreleaseworker.StepRequest
	failOnce    map[string]bool
	policyInput *scannerreleaseworker.PolicyInput
	active      atomic.Int64
	maxActive   atomic.Int64
}

// durableReplayExecutor models a process-independent backend result journal:
// replacement instances share only the workspace file and an external call
// counter, never in-memory request state.
type durableReplayExecutor struct {
	mu            sync.Mutex
	checkoutCalls *atomic.Int64
	checkout      []scannerreleaseworker.StepRequest
	delegate      *recordingExecutor
}

func (e *durableReplayExecutor) Execute(
	ctx context.Context,
	request scannerreleaseworker.StepRequest,
) (scannerreleaseworker.StepResult, error) {
	if request.Step.Key != "checkout" {
		return e.delegate.Execute(ctx, request)
	}
	e.mu.Lock()
	e.checkout = append(e.checkout, request)
	e.mu.Unlock()
	directory := filepath.Join(request.Workspace, ".test-durable-backend-results")
	path := filepath.Join(
		directory,
		strings.TrimPrefix(request.LogicalOperationID, "sha256:")+".json",
	)
	if value, err := os.ReadFile(path); err == nil {
		var result scannerreleaseworker.StepResult
		if err := json.Unmarshal(value, &result); err != nil {
			return scannerreleaseworker.StepResult{}, err
		}
		return result, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return scannerreleaseworker.StepResult{}, err
	}
	e.checkoutCalls.Add(1)
	result, err := e.delegate.Execute(ctx, request)
	if err != nil {
		return scannerreleaseworker.StepResult{}, err
	}
	value, err := json.Marshal(result)
	if err != nil {
		return scannerreleaseworker.StepResult{}, err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return scannerreleaseworker.StepResult{}, err
	}
	if err := os.WriteFile(path, value, 0o600); err != nil {
		return scannerreleaseworker.StepResult{}, err
	}
	return result, nil
}

func (e *durableReplayExecutor) checkoutRequest(
	t *testing.T,
) scannerreleaseworker.StepRequest {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.checkout) == 0 {
		t.Fatal("durable replay executor received no checkout request")
	}
	return e.checkout[len(e.checkout)-1]
}

func (e *recordingExecutor) Execute(
	ctx context.Context,
	request scannerreleaseworker.StepRequest,
) (scannerreleaseworker.StepResult, error) {
	active := e.active.Add(1)
	defer e.active.Add(-1)
	for {
		maximum := e.maxActive.Load()
		if active <= maximum || e.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	e.mu.Lock()
	if e.calls == nil {
		e.calls = make(map[string]int)
	}
	if e.requests == nil {
		e.requests = make(map[string][]scannerreleaseworker.StepRequest)
	}
	e.calls[request.Step.Key]++
	e.requests[request.Step.Key] = append(e.requests[request.Step.Key], request)
	call := e.calls[request.Step.Key]
	e.workspaces = append(e.workspaces, request.Workspace)
	shouldFail := e.failOnce[request.Step.Key] && call == 1
	e.mu.Unlock()
	if shouldFail {
		return scannerreleaseworker.StepResult{}, errors.New("transient executor failure token=super-secret")
	}
	select {
	case <-ctx.Done():
		return scannerreleaseworker.StepResult{}, ctx.Err()
	case <-time.After(20 * time.Millisecond):
	}
	result := scannerreleaseworker.StepResult{
		OutputDigest: testOutputDigest,
		Verification: scannerreleaseworker.Verification{
			DefinitionCommit: request.DefinitionCommit,
			LockDigest:       request.LockDigest,
			PolicyID:         request.PolicyID,
			PolicyRevision:   request.PolicyRevision,
		},
		Summary: map[string]any{
			"token": "super-secret",
			"log":   "request used Bearer executor-token",
		},
	}
	switch request.Step.Key {
	case "checkout":
		result.Verification.DefinitionCommit = request.DefinitionCommit
		result.Verification.LockDigest = request.LockDigest
	case "lock-reproducibility":
		result.Verification.LockDigest = request.LockDigest
	case "policy-evaluation":
		result.Verification.PolicyID = request.PolicyID
		result.Verification.PolicyRevision = request.PolicyRevision
		result.Verification.PolicyDecisionDigest = testDecisionDigest
		result.PolicyInput = e.policyInput
		if result.PolicyInput == nil {
			result.PolicyInput = passingPolicyInput()
		}
	case "candidate-evidence-summary":
		receipt := scannerrelease.PublicationReceipt{
			SchemaVersion: scannerrelease.PublicationReceiptSchema,
			CandidateID:   request.CandidateID, BuildRunID: request.BuildRunID,
			DefinitionCommit: request.DefinitionCommit, LockDigest: request.LockDigest,
			PolicyID: request.PolicyID, PolicyRevision: request.PolicyRevision,
			PolicyDecisionDigest: request.Dependencies["policy-evaluation"].OutputDigest,
			ManifestDigest:       testOutputDigest,
			ManifestURI:          "artifact://scanner-release/release-manifest@" + testOutputDigest,
			SignerIdentity:       "test://scanner-release-signer",
			Tools:                testReleaseTools(),
			Images:               testReleaseImages(),
			Artifacts:            testReleaseArtifacts(),
		}
		digest, err := scannerrelease.PublicationReceiptDigest(receipt)
		if err != nil {
			return scannerreleaseworker.StepResult{}, err
		}
		result.OutputDigest = digest
		result.Summary["publication_receipt"] = receipt
	}
	return result, nil
}

func testReleaseTools() []scannerrelease.ReleaseTool {
	tools := make([]scannerrelease.ReleaseTool, 0, len(scannerrelease.RequiredReleaseToolKeys))
	for _, key := range scannerrelease.RequiredReleaseToolKeys {
		tools = append(tools, scannerrelease.ReleaseTool{
			ToolKey: key, Version: "1.0.0", SourceReference: "locked:" + key,
			ParserCompatibility: "quality_policy:json",
			MetadataJSON:        `{"image_key":"default","kind":"wolf","integration_tier":"default","platforms":["linux/amd64","linux/arm64"],"parser_format":"json"}`,
		})
	}
	return tools
}

func testReleaseImages() []scannerrelease.ReleaseImage {
	image := func(key, kind string, platforms ...string) scannerrelease.ReleaseImage {
		platformDigests := make(map[string]string, len(platforms))
		for _, platform := range platforms {
			platformDigests[platform] = testOutputDigest
		}
		encoded, _ := json.Marshal(platformDigests)
		return scannerrelease.ReleaseImage{
			ImageKey: key, ImageKind: kind, RegistryTargetID: "test-primary",
			Repository: "test/" + key, Digest: testOutputDigest,
			PlatformDigests: string(encoded), SignatureStatus: "verified",
			SignatureDigest:            testOutputDigest,
			SignatureArtifactURI:       "oci://test/signatures@" + testOutputDigest,
			SignatureArtifactDigest:    testOutputDigest,
			SignatureMediaType:         "application/vnd.dev.cosign.simplesigning.v1+json",
			SignatureArtifactSizeBytes: 1,
			SignatureIdentity:          "test-signer", SignatureIssuer: "https://issuer.test",
			SignatureSubject: "test-subject", SignatureTrustRoot: "secret://***",
			SignatureOperationID: testOutputDigest,
			ProvenanceDigest:     testOutputDigest, SBOMDigest: testOutputDigest,
		}
	}
	return []scannerrelease.ReleaseImage{
		image("default", scannerrelease.ReleaseImageScanner, "linux/amd64", "linux/arm64"),
		image("jvm", scannerrelease.ReleaseImageScanner, "linux/amd64", "linux/arm64"),
		image("rust", scannerrelease.ReleaseImageScanner, "linux/amd64", "linux/arm64"),
		image("codeql", scannerrelease.ReleaseImageScanner, "linux/amd64"),
		image("fixer-base", scannerrelease.ReleaseImageFixer, "linux/amd64", "linux/arm64"),
		image("fixer-api", scannerrelease.ReleaseImageFixer, "linux/amd64", "linux/arm64"),
		image("fixer-claude", scannerrelease.ReleaseImageFixer, "linux/amd64", "linux/arm64"),
		image("fixer-codex", scannerrelease.ReleaseImageFixer, "linux/amd64", "linux/arm64"),
	}
}

func testReleaseArtifacts() []scannerrelease.ReleaseArtifact {
	artifacts := make([]scannerrelease.ReleaseArtifact, 0, len(scannerrelease.RequiredPublicationArtifactTypes))
	for _, artifactType := range scannerrelease.RequiredPublicationArtifactTypes {
		artifacts = append(artifacts, scannerrelease.ReleaseArtifact{
			ArtifactType: artifactType, MediaType: "application/json",
			URI:    "artifact://scanner-release/" + artifactType,
			Digest: testOutputDigest, SizeBytes: 1,
		})
	}
	return artifacts
}

func (e *recordingExecutor) lastRequest(key string) scannerreleaseworker.StepRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	requests := e.requests[key]
	if len(requests) == 0 {
		return scannerreleaseworker.StepRequest{}
	}
	return requests[len(requests)-1]
}

func passingPolicyInput() *scannerreleaseworker.PolicyInput {
	rules := scannerpolicy.Default()
	gates := make([]scannerpolicy.Gate, 0, len(rules.RequiredGates))
	for _, name := range rules.RequiredGates {
		gates = append(gates, scannerpolicy.Gate{
			Name: name, Status: scannerpolicy.GatePassed,
			EvidenceDigest: testOutputDigest,
		})
	}
	return &scannerreleaseworker.PolicyInput{
		Risk: scannerpolicy.RiskLow,
		Changes: []scannerpolicy.Change{{
			Component: "semgrep", Kind: scannerpolicy.ChangePatch,
			From: "1.0.0", To: "1.0.1",
		}},
		Gates: gates,
		Evidence: &scannerpolicy.Evidence{
			Vulnerabilities: scannerpolicy.VulnerabilityEvidence{
				DatabaseIdentity: testOutputDigest,
			},
		},
	}
}

func TestWorkerPolicyInputUsesOnlyDurableCandidateExceptions(t *testing.T) {
	t.Parallel()
	fixture := newFixtureWithPolicy(t, successPlan(), scannerpolicy.Default())
	expires := time.Now().UTC().Add(24 * time.Hour)
	approval := &scannerrelease.Approval{
		ID: "exception-durable", CandidateID: fixture.candidate.ID,
		Actor: "release-approver", Action: "exception",
		Reason: "temporary upstream advisory", ExceptionScope: "vulnerability",
		ExceptionOwner: "security-owner", CompensatingControl: "quarantine candidate",
		EvidenceDigest: testOutputDigest, ExpiresAt: &expires,
		IdempotencyKey: "exception-durable",
	}
	if err := fixture.persistence.AddApproval(context.Background(), approval); err != nil {
		t.Fatal(err)
	}
	input := *passingPolicyInput()
	input.Exceptions = []scannerpolicy.Exception{{
		ID: "executor-forged", Gate: "signature", OwnerID: "attacker",
		Reason: "forged", CompensatingControl: "none", ApprovedBy: "attacker",
		ExpiresAt: expires,
	}}
	executor := &recordingExecutor{policyInput: &input}
	worker := newWorker(t, fixture.persistence, executor, scannerreleaseworker.Config{
		WorkspaceRoot: t.TempDir(), MaxParallelSteps: 2, MaxStepAttempts: 1,
		Now: func() time.Time { return expires.Add(-24 * time.Hour) },
	})
	if processed, err := worker.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	updated, err := fixture.persistence.GetCandidate(context.Background(), fixture.candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	durable := scannerpolicy.Exception{
		ID: approval.ID, Gate: approval.ExceptionScope, OwnerID: approval.ExceptionOwner,
		Reason: approval.Reason, CompensatingControl: approval.CompensatingControl,
		ApprovedBy: approval.Actor, ExpiresAt: expires,
	}
	expected, err := scannerpolicy.Evaluate(scannerpolicy.Candidate{
		ID:               fixture.candidate.ID,
		DefinitionCommit: scannerrelease.EffectiveDefinitionCommit(fixture.candidate),
		LockDigest:       fixture.candidate.LockDigest, PolicyID: fixture.policy.ID,
		PolicyRevision: fixture.policy.Revision, CreatorID: fixture.candidate.Actor,
		Risk: input.Risk, Changes: input.Changes, Gates: input.Gates,
		Exceptions: []scannerpolicy.Exception{durable}, MaintenanceWindowOpen: true,
		Evidence: input.Evidence,
	}, scannerpolicy.Default(), expires.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if updated.PolicyDecision != expected.PolicyDecisionDigest {
		t.Fatalf("policy decision = %s, want durable-ledger decision %s", updated.PolicyDecision, expected.PolicyDecisionDigest)
	}
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func (e *recordingExecutor) callCount(key string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls[key]
}

func (e *recordingExecutor) callsSnapshot() map[string]int {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]int, len(e.calls))
	for key, value := range e.calls {
		out[key] = value
	}
	return out
}

func (e *recordingExecutor) workspacesSnapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.workspaces...)
}

type leaseLosingStore struct {
	scannerrelease.Persistence
	heartbeats atomic.Int64
	loseAt     int64
}

func (s *leaseLosingStore) HeartbeatBuildRun(
	ctx context.Context,
	id, workerID, token string,
	leaseUntil time.Time,
) (scannerrelease.BuildLeaseStatus, error) {
	if s.heartbeats.Add(1) >= s.loseAt {
		return scannerrelease.BuildLeaseStatus{}, nil
	}
	return s.Persistence.HeartbeatBuildRun(ctx, id, workerID, token, leaseUntil)
}

type fixturePersistenceStub struct {
	scannerrelease.Persistence
}

func command(key string) scannerrelease.TransitionCommand {
	return scannerrelease.TransitionCommand{
		Actor: "test", Reason: "worker test", IdempotencyKey: key, PayloadJSON: "{}",
	}
}
