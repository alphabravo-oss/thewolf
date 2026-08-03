package scannerreleasescheduler_test

import (
	"bytes"
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
	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerproposalworker"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannerreleasescheduler"
	"github.com/alphabravocompany/thewolf/internal/scannerschedule"
)

func TestDailyAndWeeklySchedulesPersistOneOperationPerPeriod(t *testing.T) {
	t.Parallel()
	fixture := newSchedulerFixture(t)
	seedCompleteDiscovery(t, fixture)
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	jobs, err := scannerreleasescheduler.DefaultJobs(scannerreleasescheduler.DefaultsConfig{
		Timezone: "UTC", DailyTime: "07:00", WeeklyTime: "07:00",
		WeeklyWeekday: now.Weekday(), DailyEnabled: true, WeeklyEnabled: true,
		DailyCatchUp: 4 * time.Hour, WeeklyCatchUp: 4 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	first := newScheduler(t, fixture.persistence, fixture.enqueuer, "replica-a", func() time.Time { return now })
	second := newScheduler(t, fixture.persistence, fixture.enqueuer, "replica-b", func() time.Time { return now })
	if err := first.Tick(context.Background(), jobs); err != nil {
		t.Fatal(err)
	}
	if err := second.Tick(context.Background(), jobs); err != nil {
		t.Fatal(err)
	}
	page, err := fixture.persistence.ListDiscoveryRuns(
		context.Background(), scannerrelease.DiscoveryFilter{}, scannerrelease.PageRequest{Limit: 20},
	)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, run := range page.Items {
		keys[run.IdempotencyKey] = true
	}
	expectedDiscovery := "scanner-schedule/daily-discovery/discovery/complete/2026-07-30"
	if !keys[expectedDiscovery] {
		t.Fatalf("missing scheduled idempotency key %q in %#v", expectedDiscovery, keys)
	}
	candidates, err := fixture.persistence.ListCandidates(
		context.Background(), scannerrelease.CandidateFilter{},
		scannerrelease.PageRequest{Limit: 20},
	)
	if err != nil {
		t.Fatal(err)
	}
	expectedCandidate := "scanner-schedule/weekly-candidate/candidate/complete/2026-07-30"
	if len(candidates.Items) != 1 ||
		candidates.Items[0].State != scannerrelease.CandidateAwaitingDefinition ||
		candidates.Items[0].IdempotencyKey != expectedCandidate ||
		!strings.Contains(candidates.Items[0].SelectionJSON, `"mode":"complete"`) ||
		strings.Contains(candidates.Items[0].SelectionJSON, "pending_latest_discovery") ||
		candidates.Items[0].RequiredGatesJSON == "[]" {
		t.Fatalf("scheduled candidate operations = %#v", candidates.Items)
	}
}

func TestWeeklyCandidateLinksLatestCompletedDiscovery(t *testing.T) {
	t.Parallel()
	fixture := newSchedulerFixture(t)
	policies, err := fixture.persistence.ListPolicies(context.Background(), "global", true)
	if err != nil || len(policies) != 1 {
		t.Fatalf("ListPolicies = %#v err=%v", policies, err)
	}
	completedAt := time.Now().UTC()
	discovery := &scannerrelease.DiscoveryRun{
		ID: uuid.NewString(), Trigger: scannerrelease.DiscoveryScheduled,
		DefinitionCommit: "0123456789abcdef",
		PolicyID:         policies[0].ID,
		PolicyRevision:   policies[0].Revision,
		ScopeJSON:        `{"mode":"complete"}`,
		State:            scannerrelease.DiscoveryCompleted,
		Actor:            "scheduler",
		IdempotencyKey:   "completed-discovery:" + uuid.NewString(),
		CompletedAt:      &completedAt,
	}
	if err := fixture.persistence.CreateDiscoveryRun(
		context.Background(), discovery,
		scannerrelease.TransitionCommand{
			Actor: "scheduler", Reason: "fixture",
			IdempotencyKey: discovery.IdempotencyKey, PayloadJSON: "{}",
		},
	); err != nil {
		t.Fatal(err)
	}
	candidateID, err := fixture.enqueuer.EnqueueScannerRelease(
		context.Background(),
		scannerreleasescheduler.Request{
			Kind: scannerreleasescheduler.KindCandidate, Scope: scannerreleasescheduler.ScopeComplete,
			Trigger: scannerrelease.DiscoveryScheduled, Actor: "scheduler",
			IdempotencyKey: "candidate-from-discovery:" + uuid.NewString(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := fixture.persistence.GetCandidate(context.Background(), candidateID)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.DiscoveryRunID != discovery.ID ||
		!strings.Contains(candidate.SelectionJSON, discovery.ID) ||
		!strings.Contains(candidate.SelectionJSON, `"mode":"complete"`) ||
		candidate.RequiredGatesJSON == "[]" {
		t.Fatalf("candidate discovery selection = %#v", candidate)
	}
}

func TestScheduledCandidateFlowsThroughProposalIntoBuildQueue(t *testing.T) {
	fixture := newSchedulerFixture(t)
	discovery := seedCompleteDiscovery(t, fixture)
	if err := fixture.persistence.AddUpdateItems(context.Background(), discovery.ID, []scannerrelease.UpdateItem{{
		ComponentType: scannerrelease.ComponentTool, ComponentName: "semgrep",
		CurrentValue: "1.0.0", AvailableValue: "1.0.1", Status: "update_available",
		RiskClass: scannerrelease.RiskLow, SelectionState: "unselected",
		SourceEvidenceJSON: `{}`, CompatibilityJSON: `{}`,
	}}); err != nil {
		t.Fatal(err)
	}
	candidateID, err := fixture.enqueuer.EnqueueScannerRelease(
		context.Background(), scannerreleasescheduler.Request{
			Kind: scannerreleasescheduler.KindCandidate, Scope: scannerreleasescheduler.ScopeComplete,
			Trigger: scannerrelease.DiscoveryScheduled, Actor: "scheduler",
			IdempotencyKey: "candidate-proposal-flow:" + uuid.NewString(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := scannerproposalworker.New(scannerproposalworker.Config{
		Store: fixture.persistence, WorkerID: "proposal-worker", Once: true,
		Proposer: proposalFunc(func(_ context.Context, request scannerproposalworker.Request) (scannerproposalworker.Result, error) {
			if request.CandidateID != candidateID || len(request.Updates) != 1 || len(request.RequiredGates) == 0 {
				t.Fatalf("proposal request = %#v", request)
			}
			return scannerproposalworker.Result{
				ProposedCommit: "0123456789abcdef0123456789abcdef01234567",
				ProposalURL:    "https://github.example.test/acme/scanners/pull/42",
				LockDigest:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				LockURI:        "oci://registry.example.test/locks@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				RiskSummary:    json.RawMessage(`{"highest_risk":"low"}`),
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce processed=%t err=%v", processed, err)
	}
	candidate, err := fixture.persistence.GetCandidate(context.Background(), candidateID)
	if err != nil || candidate.State != scannerrelease.CandidateQueued {
		t.Fatalf("candidate = %#v err=%v", candidate, err)
	}
	builds, err := fixture.persistence.ListBuildRuns(context.Background(), candidateID)
	if err != nil || len(builds) != 1 {
		t.Fatalf("builds = %#v err=%v", builds, err)
	}
}

func TestScheduledCandidateWithCurrentInputsPersistsAuditedNoOp(t *testing.T) {
	fixture := newSchedulerFixture(t)
	seedCompleteDiscovery(t, fixture)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	seedStableRelease(t, fixture, now.Add(-24*time.Hour))
	candidateID, err := fixture.enqueuer.EnqueueScannerRelease(
		context.Background(), scannerreleasescheduler.Request{
			Kind: scannerreleasescheduler.KindCandidate, Scope: scannerreleasescheduler.ScopeComplete,
			Trigger: scannerrelease.DiscoveryScheduled, Actor: "scheduler",
			IdempotencyKey: "candidate-noop-flow:" + uuid.NewString(),
			ScheduledAt:    now,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var proposerCalls atomic.Int64
	worker, err := scannerproposalworker.New(scannerproposalworker.Config{
		Store: fixture.persistence, WorkerID: "proposal-worker", Once: true,
		Proposer: proposalFunc(func(context.Context, scannerproposalworker.Request) (scannerproposalworker.Result, error) {
			proposerCalls.Add(1)
			return scannerproposalworker.Result{}, errors.New("unexpected proposal invocation")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce processed=%t err=%v", processed, err)
	}
	if proposerCalls.Load() != 0 {
		t.Fatalf("proposal executor calls = %d, want 0", proposerCalls.Load())
	}
	candidate, err := fixture.persistence.GetCandidate(context.Background(), candidateID)
	if err != nil || candidate.State != scannerrelease.CandidateRejected ||
		candidate.ErrorClass != "no_changes" {
		t.Fatalf("no-op candidate = %#v err=%v", candidate, err)
	}
	events, err := fixture.persistence.ListEvents(context.Background(), "candidate", candidateID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.EventType == "candidate.noop" {
			found = true
		}
	}
	if !found {
		t.Fatalf("candidate events = %#v", events)
	}
}

func TestScheduledCandidateWithoutFreshStableQueuesCompleteRebuild(t *testing.T) {
	fixture := newSchedulerFixture(t)
	seedCompleteDiscovery(t, fixture)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	candidateID, err := fixture.enqueuer.EnqueueScannerRelease(
		context.Background(), scannerreleasescheduler.Request{
			Kind:    scannerreleasescheduler.KindCandidate,
			Scope:   scannerreleasescheduler.ScopeComplete,
			Trigger: scannerrelease.DiscoveryScheduled,
			Actor:   "scheduler", ScheduledAt: now,
			IdempotencyKey: "candidate-required-rebuild:" + uuid.NewString(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var proposalRequest scannerproposalworker.Request
	worker, err := scannerproposalworker.New(scannerproposalworker.Config{
		Store: fixture.persistence, WorkerID: "proposal-worker", Once: true,
		Proposer: proposalFunc(func(
			_ context.Context,
			request scannerproposalworker.Request,
		) (scannerproposalworker.Result, error) {
			proposalRequest = request
			return scannerproposalworker.Result{
				ProposedCommit: "0123456789abcdef0123456789abcdef01234567",
				ProposalURL:    "https://github.example.test/acme/scanners/pull/rebuild",
				LockDigest:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				LockURI:        "oci://registry.example.test/locks@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				RiskSummary:    json.RawMessage(`{"highest_risk":"low","change":"rebuild_only"}`),
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce processed=%t err=%v", processed, err)
	}
	if len(proposalRequest.Updates) != 0 ||
		!strings.Contains(string(proposalRequest.Selection), `"force_rebuild":true`) ||
		!strings.Contains(string(proposalRequest.Selection), `"rebuild_reason":"no_stable_release"`) {
		t.Fatalf("forced rebuild proposal request = %#v", proposalRequest)
	}
	candidate, err := fixture.persistence.GetCandidate(context.Background(), candidateID)
	if err != nil || candidate.State != scannerrelease.CandidateQueued || candidate.ErrorClass != "" {
		t.Fatalf("forced rebuild candidate = %#v err=%v", candidate, err)
	}
	builds, err := fixture.persistence.ListBuildRuns(context.Background(), candidateID)
	if err != nil || len(builds) != 1 {
		t.Fatalf("forced rebuild builds = %#v err=%v", builds, err)
	}
}

func TestScheduledCandidateFreshnessControlsUnchangedRebuild(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		schedule   scannerpolicy.SchedulePolicy
		stableAt   *time.Time
		wantNoOp   bool
		wantForce  bool
		wantReason string
	}{
		{
			name: "no stable release", schedule: scannerpolicy.DefaultSchedule(),
			wantForce: true, wantReason: "no_stable_release",
		},
		{
			name: "fresh stable release", schedule: scannerpolicy.DefaultSchedule(),
			stableAt: timePointer(now.Add(-24 * time.Hour)),
			wantNoOp: true, wantReason: "stable_release_within_maximum_age",
		},
		{
			name: "maximum age reached", schedule: scannerpolicy.DefaultSchedule(),
			stableAt:  timePointer(now.Add(-7 * 24 * time.Hour)),
			wantForce: true, wantReason: "maximum_stable_image_age_exceeded",
		},
		{
			name: "explicit forced rebuild",
			schedule: func() scannerpolicy.SchedulePolicy {
				value := scannerpolicy.DefaultSchedule()
				value.ForceWeeklyRebuild = true
				return value
			}(),
			stableAt:  timePointer(now.Add(-time.Hour)),
			wantForce: true, wantReason: "policy_forced_weekly_rebuild",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSchedulerFixtureWithSchedule(t, test.schedule)
			seedCompleteDiscovery(t, fixture)
			if test.stableAt != nil {
				seedStableRelease(t, fixture, *test.stableAt)
			}
			candidateID, err := fixture.enqueuer.EnqueueScannerRelease(
				context.Background(), scannerreleasescheduler.Request{
					Kind:    scannerreleasescheduler.KindCandidate,
					Scope:   scannerreleasescheduler.ScopeComplete,
					Trigger: scannerrelease.DiscoveryScheduled,
					Actor:   "scheduler", ScheduledAt: now,
					IdempotencyKey: "freshness:" + uuid.NewString(),
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := fixture.persistence.GetCandidate(context.Background(), candidateID)
			if err != nil {
				t.Fatal(err)
			}
			var selection struct {
				NoOpIfUnchanged bool   `json:"no_op_if_unchanged"`
				ForceRebuild    bool   `json:"force_rebuild"`
				RebuildReason   string `json:"rebuild_reason"`
			}
			if err := json.Unmarshal([]byte(candidate.SelectionJSON), &selection); err != nil {
				t.Fatal(err)
			}
			if selection.NoOpIfUnchanged != test.wantNoOp ||
				selection.ForceRebuild != test.wantForce ||
				selection.RebuildReason != test.wantReason {
				t.Fatalf("selection = %#v", selection)
			}
		})
	}
}

func TestWeeklyCandidateRejectsPartialDiscoveryCoverage(t *testing.T) {
	fixture := newSchedulerFixture(t)
	policies, err := fixture.persistence.ListPolicies(context.Background(), "global", true)
	if err != nil || len(policies) != 1 {
		t.Fatalf("ListPolicies = %#v err=%v", policies, err)
	}
	completedAt := time.Now().UTC()
	partial := &scannerrelease.DiscoveryRun{
		ID: uuid.NewString(), Trigger: scannerrelease.DiscoveryScheduled,
		DefinitionCommit: "0123456789abcdef", PolicyID: policies[0].ID,
		PolicyRevision: policies[0].Revision, ScopeJSON: `{"mode":"complete"}`,
		State: scannerrelease.DiscoveryCompleted, Coverage: 0.5, TotalCount: 2,
		CoveredCount: 1, UnreachableCount: 1, ErrorClass: "partial_coverage",
		Actor: "scheduler", IdempotencyKey: "partial-discovery:" + uuid.NewString(),
		CompletedAt: &completedAt,
	}
	if err := fixture.persistence.CreateDiscoveryRun(context.Background(), partial, scannerrelease.TransitionCommand{
		Actor: "scheduler", Reason: "fixture", IdempotencyKey: partial.IdempotencyKey, PayloadJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.enqueuer.EnqueueScannerRelease(context.Background(), scannerreleasescheduler.Request{
		Kind: scannerreleasescheduler.KindCandidate, Scope: scannerreleasescheduler.ScopeComplete,
		Trigger: scannerrelease.DiscoveryScheduled, Actor: "scheduler",
		IdempotencyKey: "candidate-from-partial:" + uuid.NewString(),
	}); err == nil || !strings.Contains(err.Error(), "no complete scanner discovery") {
		t.Fatalf("partial discovery enqueue error = %v", err)
	}
	page, err := fixture.persistence.ListCandidates(context.Background(), scannerrelease.CandidateFilter{}, scannerrelease.PageRequest{Limit: 10})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("partial discovery candidates = %#v err=%v", page.Items, err)
	}
}

func TestFailedEnqueueRecoversAfterLeaseExpiry(t *testing.T) {
	t.Parallel()
	fixture := newSchedulerFixture(t)
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	enqueuer := &controlledEnqueuer{delegate: fixture.enqueuer, fail: true}
	job := scannerreleasescheduler.Job{
		Schedule: scannerschedule.Schedule{
			Key: "daily-discovery", Kind: scannerreleasescheduler.KindDiscovery, Enabled: true,
			Frequency: scannerschedule.Daily, Timezone: "UTC", Hour: 7, CatchUp: 4 * time.Hour,
		},
		Scope: scannerreleasescheduler.ScopeComplete,
	}
	observer := scannerobservability.NewRegistry()
	observer.Enable(scannerobservability.ComponentScheduler, true)
	scheduler := newObservedScheduler(
		t, fixture.persistence, enqueuer, "replica-a", clock, observer,
	)
	if err := scheduler.Tick(context.Background(), []scannerreleasescheduler.Job{job}); err == nil {
		t.Fatal("failed enqueue unexpectedly succeeded")
	}
	enqueuer.fail = false
	scheduler = newObservedScheduler(
		t, fixture.persistence, enqueuer, "replica-b", clock, observer,
	)
	if err := scheduler.Tick(context.Background(), []scannerreleasescheduler.Job{job}); err != nil {
		t.Fatal(err)
	}
	if enqueuer.successes.Load() != 0 {
		t.Fatal("schedule lease was taken before expiration")
	}
	now = now.Add(2 * time.Minute)
	if err := scheduler.Tick(context.Background(), []scannerreleasescheduler.Job{job}); err != nil {
		t.Fatal(err)
	}
	if enqueuer.successes.Load() != 1 {
		t.Fatalf("recovered enqueue successes = %d", enqueuer.successes.Load())
	}
	var metrics bytes.Buffer
	if err := observer.RenderPrometheus(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`wolf_scanner_release_lease_events_total{component="scheduler",result="reclaimed"} 1`,
		`wolf_scanner_release_retries_total{component="scheduler",reason="stale_lease"} 1`,
		`wolf_scanner_release_stuck_work{component="scheduler",kind="expired_lease"} 0`,
	} {
		if !strings.Contains(metrics.String(), expected) {
			t.Fatalf("scheduler metrics omitted %q:\n%s", expected, metrics.String())
		}
	}
}

func TestOnDemandSupportsIdempotentAndSecurityTriggers(t *testing.T) {
	t.Parallel()
	fixture := newSchedulerFixture(t)
	scheduler := newScheduler(
		t, fixture.persistence, fixture.enqueuer, "replica", time.Now,
	)
	request := scannerreleasescheduler.Request{
		Kind: scannerreleasescheduler.KindDiscovery, Scope: "selected:semgrep,gitleaks",
		Actor: "operator@example.test", IdempotencyKey: "manual/discovery/123",
	}
	first, err := scheduler.EnqueueOnDemand(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := scheduler.EnqueueOnDemand(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || second != first {
		t.Fatalf("idempotent on-demand refs = %q and %q", first, second)
	}
	security := request
	security.IdempotencyKey = "security/CVE-2026-1234"
	security.Trigger = scannerrelease.DiscoverySecurity
	if _, err := scheduler.EnqueueOnDemand(context.Background(), security); err != nil {
		t.Fatal(err)
	}
	page, _ := fixture.persistence.ListDiscoveryRuns(
		context.Background(), scannerrelease.DiscoveryFilter{}, scannerrelease.PageRequest{Limit: 10},
	)
	if len(page.Items) != 2 {
		t.Fatalf("on-demand durable operations = %d", len(page.Items))
	}
}

func TestConcurrentScheduledAndOnDemandEnqueuesRemainIndependentlyIdempotent(t *testing.T) {
	t.Parallel()
	fixture := newSchedulerFixture(t)
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	scheduled := newScheduler(
		t, fixture.persistence, fixture.enqueuer, "scheduled-replica", func() time.Time { return now },
	)
	onDemand := newScheduler(
		t, fixture.persistence, fixture.enqueuer, "api-replica", func() time.Time { return now },
	)
	jobs, err := scannerreleasescheduler.DefaultJobs(scannerreleasescheduler.DefaultsConfig{
		Timezone: "UTC", DailyTime: "07:00", DailyEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	const callers = 16
	start := make(chan struct{})
	errs := make(chan error, callers+1)
	refs := make(chan string, callers)
	var group sync.WaitGroup
	group.Add(callers + 1)
	go func() {
		defer group.Done()
		<-start
		errs <- scheduled.Tick(context.Background(), jobs[:1])
	}()
	for range callers {
		go func() {
			defer group.Done()
			<-start
			ref, enqueueErr := onDemand.EnqueueOnDemand(
				context.Background(),
				scannerreleasescheduler.Request{
					Kind: scannerreleasescheduler.KindDiscovery, Scope: scannerreleasescheduler.ScopeComplete,
					Actor: "operator@example.test", IdempotencyKey: "manual/concurrent/discovery",
				},
			)
			if enqueueErr == nil {
				refs <- ref
			}
			errs <- enqueueErr
		}()
	}
	close(start)
	group.Wait()
	close(errs)
	close(refs)
	for enqueueErr := range errs {
		if enqueueErr != nil {
			t.Fatalf("concurrent enqueue: %v", enqueueErr)
		}
	}
	var manualRef string
	for ref := range refs {
		if ref == "" {
			t.Fatal("concurrent on-demand enqueue returned an empty reference")
		}
		if manualRef == "" {
			manualRef = ref
		} else if ref != manualRef {
			t.Fatalf("on-demand idempotency produced refs %q and %q", manualRef, ref)
		}
	}

	page, err := fixture.persistence.ListDiscoveryRuns(
		context.Background(), scannerrelease.DiscoveryFilter{}, scannerrelease.PageRequest{Limit: 20},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("durable discovery operations = %d, want one scheduled and one on-demand", len(page.Items))
	}
	keys := map[string]string{}
	for _, run := range page.Items {
		keys[run.IdempotencyKey] = run.ID
	}
	const scheduledKey = "scanner-schedule/daily-discovery/discovery/complete/2026-07-30"
	if keys[scheduledKey] == "" {
		t.Fatalf("scheduled operation missing from %#v", keys)
	}
	if keys["manual/concurrent/discovery"] != manualRef {
		t.Fatalf("manual operation reference = %q, durable row = %q", manualRef, keys["manual/concurrent/discovery"])
	}
	if keys[scheduledKey] == manualRef {
		t.Fatal("scheduled and on-demand namespaces collapsed into one operation")
	}
}

func TestSchedulerHeartbeatsWhileEnqueueIsActive(t *testing.T) {
	t.Parallel()
	fixture := newSchedulerFixture(t)
	countingStore := &heartbeatCountingStore{ScheduleLeaseRepository: fixture.persistence}
	enqueuer := &controlledEnqueuer{delegate: fixture.enqueuer, delay: 45 * time.Millisecond}
	now := time.Now().UTC()
	scheduler, err := scannerreleasescheduler.New(scannerreleasescheduler.Config{
		Store: countingStore, Enqueuer: enqueuer, Owner: "replica",
		HeartbeatInterval: 10 * time.Millisecond, LeaseDuration: 100 * time.Millisecond,
		Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := scannerreleasescheduler.Job{
		Schedule: scannerschedule.Schedule{
			Key: "daily-discovery", Kind: scannerreleasescheduler.KindDiscovery, Enabled: true,
			Frequency: scannerschedule.Daily, Timezone: "UTC",
			Hour: now.Hour(), Minute: now.Minute(), CatchUp: time.Hour,
		},
		Scope: scannerreleasescheduler.ScopeComplete,
	}
	if err := scheduler.Tick(context.Background(), []scannerreleasescheduler.Job{job}); err != nil {
		t.Fatal(err)
	}
	if countingStore.count.Load() < 2 {
		t.Fatalf("schedule heartbeats = %d, want at least 2", countingStore.count.Load())
	}
}

func TestDefaultJobsRejectInvalidClock(t *testing.T) {
	t.Parallel()
	_, err := scannerreleasescheduler.DefaultJobs(scannerreleasescheduler.DefaultsConfig{
		DailyTime: "25:99", DailyEnabled: true,
	})
	if err == nil {
		t.Fatal("invalid scanner release clock accepted")
	}
}

func TestActivePolicyScheduleChangesWithoutSchedulerRestart(t *testing.T) {
	t.Parallel()
	fixture := newSchedulerFixture(t)
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	scheduler := newScheduler(
		t, fixture.persistence, fixture.enqueuer, "dynamic-replica", func() time.Time { return now },
	)
	provider := scannerreleasescheduler.ActivePolicyJobs{Store: fixture.persistence, Scope: "global"}
	createPolicy := func(revision int64, at string) {
		schedule := scannerpolicy.DefaultSchedule()
		schedule.DailyDiscovery.At = at
		schedule.DailyDiscovery.Jitter = "0s"
		disabled := false
		schedule.WeeklyCandidate.Enabled = &disabled
		scheduleJSON, err := json.Marshal(schedule)
		if err != nil {
			t.Fatal(err)
		}
		if err := fixture.persistence.CreatePolicy(context.Background(), &scannerrelease.Policy{
			ID: uuid.NewString(), Scope: "global", Revision: revision, Enabled: true,
			ScheduleJSON: string(scheduleJSON), RulesJSON: "{}", CreatedBy: "test",
		}); err != nil {
			t.Fatal(err)
		}
	}
	createPolicy(2, "09:00")
	jobs, err := provider.Jobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Tick(context.Background(), jobs); err != nil {
		t.Fatal(err)
	}
	page, _ := fixture.persistence.ListDiscoveryRuns(
		context.Background(), scannerrelease.DiscoveryFilter{}, scannerrelease.PageRequest{Limit: 10},
	)
	if len(page.Items) != 0 {
		t.Fatalf("future policy schedule enqueued %d runs", len(page.Items))
	}

	createPolicy(3, "07:00")
	jobs, err = provider.Jobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Tick(context.Background(), jobs); err != nil {
		t.Fatal(err)
	}
	page, _ = fixture.persistence.ListDiscoveryRuns(
		context.Background(), scannerrelease.DiscoveryFilter{}, scannerrelease.PageRequest{Limit: 10},
	)
	if len(page.Items) != 1 || page.Items[0].PolicyRevision != 3 {
		t.Fatalf("reloaded policy schedule runs = %#v", page.Items)
	}
}

type schedulerFixture struct {
	store       db.Store
	persistence scannerrelease.Persistence
	enqueuer    scannerreleasescheduler.PersistentEnqueuer
}

func newSchedulerFixture(t *testing.T) schedulerFixture {
	return newSchedulerFixtureWithSchedule(t, scannerpolicy.DefaultSchedule())
}

func newSchedulerFixtureWithSchedule(
	t *testing.T,
	schedule scannerpolicy.SchedulePolicy,
) schedulerFixture {
	t.Helper()
	store, err := db.NewSQLite(t.TempDir() + "/scheduler.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	persistence := store.ScannerReleases()
	rules := scannerpolicy.Default()
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
	enqueuer := scannerreleasescheduler.PersistentEnqueuer{
		Store: persistence,
		Policies: scannerreleasescheduler.LatestPolicy{
			Store: persistence, Scope: "global",
		},
		Definition: scannerreleasescheduler.StaticDefinition("0123456789abcdef"),
	}
	return schedulerFixture{store: store, persistence: persistence, enqueuer: enqueuer}
}

func seedStableRelease(t *testing.T, fixture schedulerFixture, publishedAt time.Time) {
	t.Helper()
	digest := "sha256:" + strings.Repeat("a", 64)
	id := uuid.NewString()
	policies, err := fixture.persistence.ListPolicies(context.Background(), "global", true)
	if err != nil || len(policies) != 1 {
		t.Fatalf("stable fixture policies = %#v err=%v", policies, err)
	}
	candidateID := uuid.NewString()
	candidate := &scannerrelease.Candidate{
		ID: candidateID, DefinitionCommit: "0123456789abcdef0123456789abcdef01234567",
		LockDigest: digest, SelectionJSON: `{"mode":"complete"}`,
		RiskSummaryJSON: `{}`, RequiredGatesJSON: `[]`,
		PolicyID: policies[0].ID, PolicyRevision: policies[0].Revision,
		PolicyDecision: digest, State: scannerrelease.CandidatePublished,
		Actor: "test", IdempotencyKey: "stable-candidate:" + candidateID,
	}
	if err := fixture.persistence.CreateCandidate(
		context.Background(), candidate,
		scannerrelease.TransitionCommand{
			Actor: "test", Reason: "freshness fixture candidate",
			IdempotencyKey: candidate.IdempotencyKey, PayloadJSON: "{}",
		},
	); err != nil {
		t.Fatal(err)
	}
	inventory := &scannerrelease.ReleaseInventory{Release: scannerrelease.Release{
		ID: id, Name: "scanner-set-2026.31.1", CandidateID: candidateID,
		LockDigest: digest, ManifestDigest: digest,
		ManifestURI: "oci://registry.example.test/releases@" + digest,
		State:       scannerrelease.ReleaseStable, SignerIdentity: "test-signer",
		PolicyID: policies[0].ID, PolicyRevision: policies[0].Revision,
		DefinitionCommit: "0123456789abcdef0123456789abcdef01234567",
		PublishedAt:      publishedAt, CreatedAt: publishedAt, UpdatedAt: publishedAt,
	}}
	if err := fixture.persistence.CreateRelease(
		context.Background(), inventory,
		scannerrelease.TransitionCommand{
			Actor: "test", Reason: "freshness fixture",
			IdempotencyKey: "stable-release:" + id, PayloadJSON: "{}",
		},
	); err != nil {
		t.Fatal(err)
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func seedCompleteDiscovery(t *testing.T, fixture schedulerFixture) *scannerrelease.DiscoveryRun {
	t.Helper()
	policies, err := fixture.persistence.ListPolicies(context.Background(), "global", true)
	if err != nil || len(policies) != 1 {
		t.Fatalf("ListPolicies = %#v err=%v", policies, err)
	}
	completedAt := time.Now().UTC()
	run := &scannerrelease.DiscoveryRun{
		ID: uuid.NewString(), Trigger: scannerrelease.DiscoveryScheduled,
		DefinitionCommit: "0123456789abcdef", PolicyID: policies[0].ID,
		PolicyRevision: policies[0].Revision, ScopeJSON: `{"mode":"complete"}`,
		State: scannerrelease.DiscoveryCompleted, Actor: "scheduler",
		IdempotencyKey: "complete-discovery:" + uuid.NewString(), CompletedAt: &completedAt,
	}
	if err := fixture.persistence.CreateDiscoveryRun(
		context.Background(), run, scannerrelease.TransitionCommand{
			Actor: "scheduler", Reason: "fixture", IdempotencyKey: run.IdempotencyKey,
			PayloadJSON: "{}",
		},
	); err != nil {
		t.Fatal(err)
	}
	return run
}

func newScheduler(
	t *testing.T,
	store scannerrelease.ScheduleLeaseRepository,
	enqueuer scannerreleasescheduler.Enqueuer,
	owner string,
	now func() time.Time,
) *scannerreleasescheduler.Scheduler {
	t.Helper()
	scheduler, err := scannerreleasescheduler.New(scannerreleasescheduler.Config{
		Store: store, Enqueuer: enqueuer, Owner: owner,
		LeaseDuration: time.Minute, HeartbeatInterval: 20 * time.Second, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return scheduler
}

func newObservedScheduler(
	t *testing.T,
	store scannerrelease.ScheduleLeaseRepository,
	enqueuer scannerreleasescheduler.Enqueuer,
	owner string,
	now func() time.Time,
	observer scannerobservability.Observer,
) *scannerreleasescheduler.Scheduler {
	t.Helper()
	scheduler, err := scannerreleasescheduler.New(scannerreleasescheduler.Config{
		Store: store, Enqueuer: enqueuer, Owner: owner,
		LeaseDuration: time.Minute, HeartbeatInterval: 20 * time.Second,
		Now: now, Observer: observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return scheduler
}

type controlledEnqueuer struct {
	delegate  scannerreleasescheduler.Enqueuer
	mu        sync.Mutex
	fail      bool
	delay     time.Duration
	successes atomic.Int64
}

type proposalFunc func(context.Context, scannerproposalworker.Request) (scannerproposalworker.Result, error)

func (function proposalFunc) Propose(
	ctx context.Context,
	request scannerproposalworker.Request,
) (scannerproposalworker.Result, error) {
	return function(ctx, request)
}

func (e *controlledEnqueuer) EnqueueScannerRelease(
	ctx context.Context,
	request scannerreleasescheduler.Request,
) (string, error) {
	e.mu.Lock()
	fail := e.fail
	delay := e.delay
	e.mu.Unlock()
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	if fail {
		return "", errors.New("simulated enqueue outage")
	}
	ref, err := e.delegate.EnqueueScannerRelease(ctx, request)
	if err == nil {
		e.successes.Add(1)
	}
	return ref, err
}

type heartbeatCountingStore struct {
	scannerrelease.ScheduleLeaseRepository
	count atomic.Int64
}

func (s *heartbeatCountingStore) HeartbeatScheduleLease(
	ctx context.Context,
	scheduleKey, periodKey, owner, token string,
	now, expires time.Time,
) (bool, error) {
	s.count.Add(1)
	return s.ScheduleLeaseRepository.HeartbeatScheduleLease(
		ctx, scheduleKey, periodKey, owner, token, now, expires,
	)
}
