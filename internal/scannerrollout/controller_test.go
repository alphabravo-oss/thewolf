package scannerrollout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestControllerProgressesCanaryThenStable(t *testing.T) {
	t.Parallel()
	clock := newControllerClock()
	store := newControllerStore(policySnapshot(t, false), true)
	runtime := &controllerRuntime{clock: clock}
	controller := newTestController(t, store, runtime, clock, nil)

	runUntilState(t, controller, store, clock, scannerrelease.RolloutCompleted)
	assignments := runtime.assignmentSnapshot()
	if len(assignments) != 2 ||
		assignments[0].CohortName != "canary" ||
		assignments[1].CohortName != "stable" ||
		assignments[0].Rollback || assignments[1].Rollback {
		t.Fatalf("assignments = %#v", assignments)
	}
	assignmentOperations := make(map[string]string, len(assignments))
	for _, assignment := range assignments {
		assignmentOperations[assignment.CohortName] = assignment.OperationID
	}
	for _, request := range runtime.healthSnapshot() {
		if request.OperationID == "" ||
			request.OperationID != assignmentOperations[request.CohortName] {
			t.Fatalf("health request was not bound to its assignment: %#v", request)
		}
	}
	cohorts := store.cohortSnapshot()
	for _, cohort := range cohorts {
		if cohort.State != CohortCompleted || cohort.CompletedAt == nil ||
			cohort.HealthObservedAt == nil || cohort.ObservedReleaseID != "new" {
			t.Fatalf("completed cohort = %#v", cohort)
		}
	}
}

func TestControllerAutomaticallyRollsBackInReverseCohortOrder(t *testing.T) {
	t.Parallel()
	clock := newControllerClock()
	store := newControllerStore(policySnapshot(t, false), true)
	runtime := &controllerRuntime{
		clock: clock,
		health: func(request HealthRequest, now time.Time) HealthSnapshot {
			snapshot := healthySnapshot(request.DesiredReleaseID, now)
			if request.DesiredReleaseID == "new" {
				snapshot.Canary.SignatureFailures = 1
			}
			return snapshot
		},
	}
	controller := newTestController(t, store, runtime, clock, nil)

	runUntilState(t, controller, store, clock, scannerrelease.RolloutRolledBack)
	var rollback []AssignmentRequest
	for _, assignment := range runtime.assignmentSnapshot() {
		if assignment.Rollback {
			rollback = append(rollback, assignment)
		}
	}
	if len(rollback) != 2 ||
		rollback[0].CohortName != "stable" ||
		rollback[1].CohortName != "canary" ||
		rollback[0].DesiredReleaseID != "old" ||
		rollback[1].DesiredReleaseID != "old" {
		t.Fatalf("rollback assignments = %#v", rollback)
	}
}

func TestControllerFaultInjectionRestoresPriorReleaseForEveryAutomaticClass(
	t *testing.T,
) {
	tests := []struct {
		name   string
		inject func(*HealthSnapshot)
	}{
		{
			name: "signature verification",
			inject: func(snapshot *HealthSnapshot) {
				snapshot.Canary.SignatureFailures = 1
			},
		},
		{
			name: "manifest digest verification",
			inject: func(snapshot *HealthSnapshot) {
				snapshot.Canary.ManifestFailures = 1
			},
		},
		{
			name: "image pull",
			inject: func(snapshot *HealthSnapshot) {
				snapshot.Canary.PullFailures = 1
			},
		},
		{
			name: "worker crash loop",
			inject: func(snapshot *HealthSnapshot) {
				snapshot.Canary.CrashLoops = 1
			},
		},
		{
			name: "infrastructure regression",
			inject: func(snapshot *HealthSnapshot) {
				snapshot.Canary.Samples = 100
				snapshot.Canary.InfrastructureFailures = 5
				snapshot.Canary.StableSamples = 100
				snapshot.Canary.StableInfrastructureFailures = 0
			},
		},
		{
			name: "parser regression",
			inject: func(snapshot *HealthSnapshot) {
				snapshot.Canary.ParserFailures = 1
			},
		},
		{
			name: "expected finding collapse",
			inject: func(snapshot *HealthSnapshot) {
				snapshot.Canary.ExpectedFindingLosses = 1
			},
		},
		{
			name: "duration regression",
			inject: func(snapshot *HealthSnapshot) {
				snapshot.Canary.CandidateP95Duration = 2 * time.Second
				snapshot.Canary.StableP95Duration = time.Second
			},
		},
		{
			name: "canary deadline",
			inject: func(snapshot *HealthSnapshot) {
				*snapshot = HealthSnapshot{ObservedAt: snapshot.ObservedAt}
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			clock := newControllerClock()
			store := newControllerStore(policySnapshot(t, false), true)
			runtime := &controllerRuntime{
				clock: clock,
				health: func(request HealthRequest, now time.Time) HealthSnapshot {
					snapshot := healthySnapshot(request.DesiredReleaseID, now)
					if request.DesiredReleaseID == "new" {
						tt.inject(&snapshot)
					}
					return snapshot
				},
			}
			controller, err := NewController(Config{
				Store: store, Runtime: runtime, WorkerID: "controller-a",
				PollInterval: time.Second, ReconcileInterval: time.Second,
				HeartbeatInterval: 5 * time.Second, LeaseDuration: 30 * time.Second,
				CohortTimeout: time.Second, Now: clock.Now,
			})
			if err != nil {
				t.Fatal(err)
			}

			runUntilState(
				t, controller, store, clock, scannerrelease.RolloutRollingBack,
			)
			recoveryStarted := clock.Now()
			wallStarted := time.Now()
			clock.Advance(2 * time.Second)
			runUntilState(
				t, controller, store, clock, scannerrelease.RolloutRolledBack,
			)
			logicalRecovery := clock.Now().Sub(recoveryStarted)
			wallRecovery := time.Since(wallStarted)
			if logicalRecovery > 30*time.Second {
				t.Fatalf("logical rollback recovery took %s", logicalRecovery)
			}
			if wallRecovery > 2*time.Second {
				t.Fatalf("in-test rollback recovery took %s", wallRecovery)
			}
			var restored []AssignmentRequest
			for _, assignment := range runtime.assignmentSnapshot() {
				if assignment.Rollback {
					restored = append(restored, assignment)
				}
			}
			if len(restored) != 2 ||
				restored[0].CohortName != "stable" ||
				restored[1].CohortName != "canary" ||
				restored[0].DesiredReleaseID != "old" ||
				restored[1].DesiredReleaseID != "old" {
				t.Fatalf("restoration assignments = %#v", restored)
			}
			t.Logf(
				"measured deterministic rollback recovery: logical=%s wall=%s",
				logicalRecovery, wallRecovery,
			)
		})
	}
}

func TestControllerRollsBackStuckCohortAfterDeadline(t *testing.T) {
	t.Parallel()
	clock := newControllerClock()
	store := newControllerStore(policySnapshot(t, false), true)
	runtime := &controllerRuntime{
		clock: clock,
		health: func(_ HealthRequest, now time.Time) HealthSnapshot {
			return HealthSnapshot{ObservedAt: now}
		},
	}
	controller, err := NewController(Config{
		Store: store, Runtime: runtime, WorkerID: "controller-a",
		PollInterval: time.Second, ReconcileInterval: time.Second,
		HeartbeatInterval: 5 * time.Second, LeaseDuration: 30 * time.Second,
		CohortTimeout: time.Second, Now: clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Second)
	if _, err := controller.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Second)
	if _, err := controller.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state := store.rolloutSnapshot().State; state != scannerrelease.RolloutRollingBack {
		t.Fatalf("stuck rollout state = %s", state)
	}
}

func TestControllerPausesWhenMaintenanceGateIsClosed(t *testing.T) {
	t.Parallel()
	clock := newControllerClock()
	store := newControllerStore(policySnapshot(t, true), true)
	runtime := &controllerRuntime{clock: clock}
	controller := newTestController(t, store, runtime, clock, nil)

	processed, err := controller.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce = %t, %v", processed, err)
	}
	if state := store.rolloutSnapshot().State; state != scannerrelease.RolloutPaused {
		t.Fatalf("rollout state = %s", state)
	}
	if assignments := runtime.assignmentSnapshot(); len(assignments) != 0 {
		t.Fatalf("closed maintenance gate assigned cohorts: %#v", assignments)
	}
}

func TestControllerFailsClosedOnInvalidPolicySnapshot(t *testing.T) {
	t.Parallel()
	clock := newControllerClock()
	store := newControllerStore(`{}`, true)
	controller := newTestController(
		t, store, &controllerRuntime{clock: clock}, clock, nil,
	)
	if processed, err := controller.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("RunOnce = %t, %v", processed, err)
	}
	if state := store.rolloutSnapshot().State; state != scannerrelease.RolloutFailed {
		t.Fatalf("invalid-policy rollout state = %s", state)
	}
}

func TestControllerRollbackDoesNotDependOnHealthyPromotionPolicy(t *testing.T) {
	t.Parallel()
	clock := newControllerClock()
	store := newControllerStore(`{}`, true)
	store.rollout.State = scannerrelease.RolloutRollingBack
	runtime := &controllerRuntime{clock: clock}
	controller := newTestController(t, store, runtime, clock, nil)

	runUntilState(t, controller, store, clock, scannerrelease.RolloutRolledBack)
}

func TestControllerExternalPauseCancelsRuntimeAndReleasesClaim(t *testing.T) {
	t.Parallel()
	clock := newControllerClock()
	store := newControllerStore(policySnapshot(t, false), true)
	store.rollout.State = scannerrelease.RolloutPreparing
	blocking := &controllerLifecycleRuntime{
		controllerRuntime: &controllerRuntime{
			clock:   clock,
			started: make(chan struct{}),
			block:   true,
		},
	}
	controller, err := NewController(Config{
		Store: store, Runtime: blocking, WorkerID: "controller-a",
		PollInterval: time.Millisecond, ReconcileInterval: time.Millisecond,
		HeartbeatInterval: 5 * time.Millisecond, LeaseDuration: time.Second,
		CohortTimeout: time.Minute, Now: clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := controller.RunOnce(context.Background())
		result <- err
	}()
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("runtime assignment did not start")
	}
	current := store.rolloutSnapshot()
	if _, err := store.TransitionRollout(
		context.Background(), current.ID, current.Version, scannerrelease.RolloutPaused,
		scannerrelease.TransitionCommand{
			Actor: "operator", Reason: "operator pause", IdempotencyKey: "pause",
		},
	); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("RunOnce after pause: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("controller did not cooperatively cancel runtime assignment")
	}
	if !blocking.wasCancelled() {
		t.Fatal("runtime did not observe context cancellation")
	}
	if store.activeClaim() {
		t.Fatal("controller left its claim active after external pause")
	}
	clock.Advance(2 * time.Millisecond)
	if processed, err := controller.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("paused lifecycle reconciliation = %t, %v", processed, err)
	}
	pauses := blocking.lifecycleSnapshot("pause")
	if len(pauses) != 1 ||
		pauses[0].DesiredReleaseID != "new" {
		t.Fatalf("paused lifecycle requests = %#v", pauses)
	}
}

func TestControllerReconcilesResumeForAcceptedDeployment(t *testing.T) {
	t.Parallel()
	clock := newControllerClock()
	store := newControllerStore(policySnapshot(t, false), true)
	store.rollout.State = scannerrelease.RolloutPaused
	store.cohorts[0].State = CohortObserving
	store.cohorts[0].DesiredReleaseID = "new"
	runtime := &controllerLifecycleRuntime{
		controllerRuntime: &controllerRuntime{clock: clock},
	}
	controller := newTestController(t, store, runtime, clock, nil)

	if processed, err := controller.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("paused lifecycle reconciliation = %t, %v", processed, err)
	}
	pauses := runtime.lifecycleSnapshot("pause")
	if len(pauses) != 1 {
		t.Fatalf("paused lifecycle requests = %#v", pauses)
	}
	current := store.rolloutSnapshot()
	if _, err := store.TransitionRollout(
		context.Background(), current.ID, current.Version,
		scannerrelease.RolloutCanary,
		scannerrelease.TransitionCommand{
			Actor: "operator", Reason: "operator resume", IdempotencyKey: "resume",
		},
	); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Second)
	if processed, err := controller.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("resumed lifecycle reconciliation = %t, %v", processed, err)
	}
	if resumes := runtime.lifecycleSnapshot("resume"); len(resumes) != 1 ||
		resumes[0].OperationID != pauses[0].OperationID {
		t.Fatalf("resumed lifecycle requests = %#v", resumes)
	}
}

func TestControllerLeaseLossCancelsRuntimeWithoutLatePersistence(t *testing.T) {
	t.Parallel()
	clock := newControllerClock()
	store := newControllerStore(policySnapshot(t, false), true)
	store.rollout.State = scannerrelease.RolloutPreparing
	blocking := &controllerRuntime{
		clock:   clock,
		started: make(chan struct{}),
		block:   true,
	}
	controller, err := NewController(Config{
		Store: store, Runtime: blocking, WorkerID: "controller-a",
		PollInterval: time.Millisecond, ReconcileInterval: time.Millisecond,
		HeartbeatInterval: 5 * time.Millisecond, LeaseDuration: time.Second,
		CohortTimeout: time.Minute, Now: clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := controller.RunOnce(context.Background())
		result <- err
	}()
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("runtime assignment did not start")
	}
	clock.Advance(2 * time.Second)
	select {
	case err := <-result:
		if !errors.Is(err, ErrRolloutLeaseLost) {
			t.Fatalf("RunOnce error = %v, want lease loss", err)
		}
	case <-time.After(time.Second):
		t.Fatal("controller did not cancel after lease expiry")
	}
	cohorts := store.cohortSnapshot()
	if cohorts[0].State != CohortAssigning || cohorts[0].HealthObservedAt != nil {
		t.Fatalf("late result was persisted after lease loss: %#v", cohorts[0])
	}
	firstAssignments := blocking.assignmentSnapshot()
	if len(firstAssignments) != 1 || firstAssignments[0].OperationID == "" {
		t.Fatalf("first-owner assignments = %#v", firstAssignments)
	}

	restartedRuntime := &controllerRuntime{clock: clock}
	restarted, err := NewController(Config{
		Store: store, Runtime: restartedRuntime, WorkerID: "controller-b",
		PollInterval: time.Millisecond, ReconcileInterval: time.Millisecond,
		HeartbeatInterval: 5 * time.Millisecond, LeaseDuration: time.Second,
		CohortTimeout: time.Minute, Now: clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := restarted.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("restarted RunOnce = %t, %v", processed, err)
	}
	reclaimed := restartedRuntime.assignmentSnapshot()
	if len(reclaimed) != 1 ||
		reclaimed[0].OperationID != firstAssignments[0].OperationID {
		t.Fatalf("reclaimed assignment = %#v; first = %#v", reclaimed, firstAssignments)
	}
	if claim := store.claimSnapshot(); claim.Attempt != 2 || !claim.Reclaimed {
		t.Fatalf("reclaimed claim = %#v", claim)
	}
	if state := store.rolloutSnapshot().State; state != scannerrelease.RolloutCanary {
		t.Fatalf("rollout state after reclaim = %s", state)
	}
}

func TestControllerRetriesAssignmentWithStableOperationID(t *testing.T) {
	t.Parallel()
	clock := newControllerClock()
	store := newControllerStore(policySnapshot(t, false), true)
	store.rollout.State = scannerrelease.RolloutPreparing
	store.failObservingOnce = true
	runtime := &controllerRuntime{clock: clock}
	controller := newTestController(t, store, runtime, clock, nil)

	if processed, err := controller.RunOnce(context.Background()); err == nil || !processed {
		t.Fatalf("first RunOnce = %t, %v; want persisted assigning plus update failure", processed, err)
	}
	clock.Advance(2 * time.Second)
	if processed, err := controller.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("retry RunOnce = %t, %v", processed, err)
	}
	assignments := runtime.assignmentSnapshot()
	if len(assignments) != 2 || assignments[0].OperationID == "" ||
		assignments[0].OperationID != assignments[1].OperationID {
		t.Fatalf("retry assignments = %#v", assignments)
	}
	if state := store.rolloutSnapshot().State; state != scannerrelease.RolloutCanary {
		t.Fatalf("rollout state after retry = %s", state)
	}
}

func TestControllerCancelsCandidateDeploymentBeforeRollbackRestoration(t *testing.T) {
	t.Parallel()
	clock := newControllerClock()
	store := newControllerStore(policySnapshot(t, false), true)
	store.rollout.State = scannerrelease.RolloutRollingBack
	store.cohorts[0].State = CohortObserving
	store.cohorts[0].DesiredReleaseID = "new"
	runtime := &controllerLifecycleRuntime{
		controllerRuntime: &controllerRuntime{clock: clock},
	}
	controller := newTestController(t, store, runtime, clock, nil)

	if processed, err := controller.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("rollback reconciliation = %t, %v", processed, err)
	}
	cancelled := runtime.lifecycleSnapshot("cancel")
	if len(cancelled) != 1 || cancelled[0].CohortName != "canary" ||
		cancelled[0].DesiredReleaseID != "new" {
		t.Fatalf("candidate cancellation requests = %#v", cancelled)
	}
	assignments := runtime.assignmentSnapshot()
	if len(assignments) != 1 || !assignments[0].Rollback ||
		assignments[0].CohortName != "stable" ||
		assignments[0].DesiredReleaseID != "old" {
		t.Fatalf("rollback restoration assignments = %#v", assignments)
	}
}

func TestControllerCancelUsesRollbackOrExplicitFailure(t *testing.T) {
	t.Parallel()
	clock := newControllerClock()
	store := newControllerStore(policySnapshot(t, false), true)
	controller := newTestController(t, store, &controllerRuntime{clock: clock}, clock, nil)

	current := store.rolloutSnapshot()
	cancelled, err := controller.Cancel(
		context.Background(), current.ID, current.Version,
		"operator", "cancel deployment", "cancel-1",
	)
	if err != nil || cancelled.State != scannerrelease.RolloutRollingBack {
		t.Fatalf("cancelled rollout = %#v, err = %v", cancelled, err)
	}

	store = newControllerStore(policySnapshot(t, false), false)
	controller = newTestController(t, store, &controllerRuntime{clock: clock}, clock, nil)
	current = store.rolloutSnapshot()
	cancelled, err = controller.Cancel(
		context.Background(), current.ID, current.Version,
		"operator", "cancel initial deployment", "cancel-2",
	)
	if err != nil || cancelled.State != scannerrelease.RolloutFailed {
		t.Fatalf("initial rollout cancellation = %#v, err = %v", cancelled, err)
	}
}

func TestWorkerStatusRuntimeAggregatesAssignmentAndHealth(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	oldAssignedAt := now.Add(-10 * time.Minute)
	oldEvidenceAt := now.Add(-time.Second)
	statusStore := &fakeWorkerStatusStore{statuses: []scannerrelease.WorkerReleaseStatus{
		{
			WorkerID: "canary-1", Cohort: "canary", DesiredReleaseID: "old",
			ObservedReleaseID: "new", VerificationState: "verified",
			CapabilitiesJSON:      `{"samples":7,"p95_duration_ms":1200}`,
			AssignmentOperationID: "old-canary-assignment",
			AssignedAt:            &oldAssignedAt, EvidenceObservedAt: &oldEvidenceAt,
			LastHeartbeat: now,
		},
		{
			WorkerID: "stable-1", Cohort: "stable", DesiredReleaseID: "old",
			ObservedReleaseID: "old", VerificationState: "verified",
			CapabilitiesJSON:      `{"samples":9,"p95_duration_ms":1000}`,
			AssignmentOperationID: "stable-assignment",
			AssignedAt:            &oldAssignedAt, EvidenceObservedAt: &oldEvidenceAt,
			LastHeartbeat: now,
		},
	}}
	runtime := WorkerStatusRuntime{
		Store: statusStore, ActiveWithin: time.Minute, Now: func() time.Time { return now },
	}
	if err := runtime.Assign(context.Background(), AssignmentRequest{
		OperationID: "new-canary-assignment",
		CohortName:  "canary", DesiredReleaseID: "new",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtime.Health(context.Background(), HealthRequest{
		OperationID: "new-canary-assignment",
		CohortName:  "canary", StableCohortName: "stable", DesiredReleaseID: "new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.TotalWorkers != 1 || snapshot.ReadyWorkers != 0 ||
		snapshot.ObservedReleaseID != "" ||
		snapshot.Canary.Samples != 0 || snapshot.Canary.StableSamples != 9 ||
		snapshot.Canary.CandidateP95Duration != 0 ||
		snapshot.Canary.StableP95Duration != time.Second {
		t.Fatalf("pre-assignment evidence was accepted: %#v", snapshot)
	}
	if snapshot.RealScans == nil ||
		snapshot.RealScans.State != "pending" ||
		snapshot.RealScans.CandidateSamples != 0 ||
		snapshot.RealScans.StableSamples != 9 ||
		snapshot.RealScans.StableP95DurationMS != 1000 {
		t.Fatalf("pre-assignment real-scan projection = %#v", snapshot.RealScans)
	}

	status := statusStore.workerSnapshot(t, "canary-1")
	status.ObservedReleaseID = "new"
	status.VerificationState = "verified"
	status.CapabilitiesJSON = `{"samples":7,"p95_duration_ms":1200}`
	status.EvidenceObservedAt = &oldEvidenceAt
	if err := statusStore.UpsertWorkerReleaseStatus(context.Background(), &status); err != nil {
		t.Fatal(err)
	}
	snapshot, err = runtime.Health(context.Background(), HealthRequest{
		OperationID: "new-canary-assignment",
		CohortName:  "canary", StableCohortName: "stable", DesiredReleaseID: "new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ReadyWorkers != 0 || snapshot.ObservedReleaseID != "" ||
		snapshot.Canary.Samples != 0 {
		t.Fatalf("evidence timestamped before assignment was accepted: %#v", snapshot)
	}

	evidenceAt := now.Add(time.Second)
	status.EvidenceObservedAt = &evidenceAt
	status.LastHeartbeat = evidenceAt
	if err := statusStore.UpsertWorkerReleaseStatus(context.Background(), &status); err != nil {
		t.Fatal(err)
	}
	snapshot, err = runtime.Health(context.Background(), HealthRequest{
		OperationID: "new-canary-assignment",
		CohortName:  "canary", StableCohortName: "stable", DesiredReleaseID: "new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ReadyWorkers != 1 || snapshot.ObservedReleaseID != "new" ||
		snapshot.Canary.Samples != 7 ||
		snapshot.Canary.CandidateP95Duration != 1200*time.Millisecond {
		t.Fatalf("post-assignment evidence was not accepted: %#v", snapshot)
	}
	if snapshot.RealScans == nil ||
		snapshot.RealScans.State != "healthy" ||
		snapshot.RealScans.CandidateSamples != 7 ||
		snapshot.RealScans.StableSamples != 9 ||
		snapshot.RealScans.CandidateP95DurationMS != 1200 ||
		snapshot.RealScans.WorkersTotal != 1 ||
		snapshot.RealScans.WorkersReady != 1 {
		t.Fatalf("post-assignment real-scan projection = %#v", snapshot.RealScans)
	}

	if err := runtime.Assign(context.Background(), AssignmentRequest{
		OperationID: "new-canary-assignment",
		CohortName:  "canary", DesiredReleaseID: "new",
	}); err != nil {
		t.Fatal(err)
	}
	status = statusStore.workerSnapshot(t, "canary-1")
	if status.ObservedReleaseID != "new" || status.EvidenceObservedAt == nil {
		t.Fatalf("idempotent assignment cleared current evidence: %#v", status)
	}

	if err := runtime.Assign(context.Background(), AssignmentRequest{
		OperationID: "second-canary-assignment",
		CohortName:  "canary", DesiredReleaseID: "new",
	}); err != nil {
		t.Fatal(err)
	}
	status = statusStore.workerSnapshot(t, "canary-1")
	if status.ObservedReleaseID != "" || status.VerificationState != "pending" ||
		status.CapabilitiesJSON != "{}" || status.CachedDigestsJSON != "[]" ||
		status.EvidenceObservedAt != nil {
		t.Fatalf("distinct same-release assignment retained evidence: %#v", status)
	}
}

func newTestController(
	t *testing.T,
	store *controllerStore,
	runtime Runtime,
	clock *controllerClock,
	gate ProgressGate,
) *Controller {
	t.Helper()
	controller, err := NewController(Config{
		Store: store, Runtime: runtime, Gate: gate, WorkerID: "controller-a",
		PollInterval: time.Second, ReconcileInterval: time.Second,
		HeartbeatInterval: 5 * time.Second, LeaseDuration: 30 * time.Second,
		CohortTimeout: time.Minute, Now: clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return controller
}

func runUntilState(
	t *testing.T,
	controller *Controller,
	store *controllerStore,
	clock *controllerClock,
	target scannerrelease.RolloutState,
) {
	t.Helper()
	for attempt := 0; attempt < 24; attempt++ {
		processed, err := controller.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce %d: %v", attempt, err)
		}
		if !processed {
			t.Fatalf("RunOnce %d found no due rollout in state %s", attempt, store.rolloutSnapshot().State)
		}
		if store.rolloutSnapshot().State == target {
			return
		}
		clock.Advance(2 * time.Second)
	}
	t.Fatalf("rollout did not reach %s: %#v", target, store.rolloutSnapshot())
}

func policySnapshot(t *testing.T, maintenanceClosed bool) string {
	t.Helper()
	policy := scannerpolicy.Default()
	policy.Canary.MinimumSamples = 1
	policy.Canary.ObservationText = time.Second.String()
	policy.Rollback.Automatic = true
	encoded, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if !maintenanceClosed {
		return string(encoded)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot["maintenance"] = map[string]any{
		"required": true, "window_open": false,
	}
	encoded, err = json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func healthySnapshot(releaseID string, now time.Time) HealthSnapshot {
	return HealthSnapshot{
		ObservedReleaseID: releaseID, TotalWorkers: 1, ReadyWorkers: 1,
		Canary: CanaryHealth{
			Samples: 2, StableSamples: 2,
			CandidateP95Duration: time.Second, StableP95Duration: time.Second,
		},
		ObservedAt: now,
	}
}

type controllerClock struct {
	mu  sync.Mutex
	now time.Time
}

func newControllerClock() *controllerClock {
	return &controllerClock{now: time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)}
}

func (c *controllerClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *controllerClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

type controllerStore struct {
	scannerrelease.Persistence

	mu       sync.Mutex
	rollout  scannerrelease.Rollout
	cohorts  []scannerrelease.RolloutCohort
	releases map[string]scannerrelease.Release
	claim    *scannerrelease.RolloutClaim
	counter  int

	failObservingOnce bool
}

func newControllerStore(policy string, withPrevious bool) *controllerStore {
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	rollout := scannerrelease.Rollout{
		ID: "rollout-1", Target: "production", ToReleaseID: "new",
		Strategy: "canary_then_stable", State: scannerrelease.RolloutPending,
		PolicySnapshotJSON: policy, Actor: "operator", Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	releases := map[string]scannerrelease.Release{
		"new": {
			ID: "new", State: scannerrelease.ReleasePublished,
			RollbackEligible: true,
		},
	}
	if withPrevious {
		rollout.FromReleaseID = "old"
		releases["old"] = scannerrelease.Release{
			ID: "old", State: scannerrelease.ReleaseStable,
			RollbackEligible: true,
		}
	}
	return &controllerStore{
		rollout: rollout,
		cohorts: []scannerrelease.RolloutCohort{
			{
				ID: "cohort-canary", RolloutID: rollout.ID, Name: "canary",
				Ordinal: 0, DesiredReleaseID: "new", State: CohortPending,
				HealthSummaryJSON: `{}`, Version: 1, CreatedAt: now, UpdatedAt: now,
			},
			{
				ID: "cohort-stable", RolloutID: rollout.ID, Name: "stable",
				Ordinal: 1, DesiredReleaseID: "new", State: CohortPending,
				HealthSummaryJSON: `{}`, Version: 1, CreatedAt: now, UpdatedAt: now,
			},
		},
		releases: releases,
	}
}

func (s *controllerStore) ClaimNextRollout(
	_ context.Context,
	workerID string,
	now, leaseUntil time.Time,
) (*scannerrelease.RolloutClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !controllerEligible(s.rollout.State) {
		return nil, nil
	}
	if s.claim != nil && ((s.claim.State == scannerrelease.RolloutClaimActive &&
		s.claim.LeaseExpires.After(now)) ||
		(s.claim.State == scannerrelease.RolloutClaimReleased &&
			s.claim.AvailableAt.After(now))) {
		return nil, nil
	}
	reclaimed := s.claim != nil &&
		s.claim.State == scannerrelease.RolloutClaimActive &&
		!s.claim.LeaseExpires.After(now)
	s.counter++
	created := now
	attempt := 1
	if s.claim != nil {
		created = s.claim.CreatedAt
		attempt = s.claim.Attempt + 1
	}
	s.claim = &scannerrelease.RolloutClaim{
		RolloutID: s.rollout.ID, WorkerID: workerID,
		LeaseToken:   fmt.Sprintf("token-%d", s.counter),
		State:        scannerrelease.RolloutClaimActive,
		LeaseExpires: leaseUntil, HeartbeatAt: now, AvailableAt: now,
		Attempt: attempt, Version: int64(attempt), CreatedAt: created, UpdatedAt: now,
		Reclaimed: reclaimed,
	}
	copy := *s.claim
	return &copy, nil
}

func (s *controllerStore) HeartbeatRollout(
	_ context.Context,
	rolloutID, workerID, token string,
	now, leaseUntil time.Time,
) (scannerrelease.RolloutLeaseStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claim == nil || s.claim.RolloutID != rolloutID ||
		s.claim.WorkerID != workerID || s.claim.LeaseToken != token ||
		s.claim.State != scannerrelease.RolloutClaimActive ||
		!s.claim.LeaseExpires.After(now) {
		return scannerrelease.RolloutLeaseStatus{}, nil
	}
	s.claim.HeartbeatAt = now
	s.claim.LeaseExpires = leaseUntil
	return scannerrelease.RolloutLeaseStatus{
		Current: true, RolloutVersion: s.rollout.Version, State: s.rollout.State,
	}, nil
}

func (s *controllerStore) ReleaseRolloutClaim(
	_ context.Context,
	rolloutID, workerID, token string,
	now, availableAt time.Time,
	_ scannerrelease.TransitionCommand,
) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claim == nil || s.claim.RolloutID != rolloutID ||
		s.claim.WorkerID != workerID || s.claim.LeaseToken != token ||
		s.claim.State != scannerrelease.RolloutClaimActive ||
		!s.claim.LeaseExpires.After(now) {
		return false, nil
	}
	s.claim.State = scannerrelease.RolloutClaimReleased
	s.claim.AvailableAt = availableAt
	return true, nil
}

func (s *controllerStore) GetRollout(_ context.Context, id string) (*scannerrelease.Rollout, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.rollout.ID {
		return nil, errors.New("rollout not found")
	}
	copy := s.rollout
	return &copy, nil
}

func (s *controllerStore) TransitionRollout(
	_ context.Context,
	id string,
	expected int64,
	target scannerrelease.RolloutState,
	_ scannerrelease.TransitionCommand,
) (*scannerrelease.Rollout, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.rollout.ID {
		return nil, errors.New("rollout not found")
	}
	if expected != s.rollout.Version {
		return nil, scannerrelease.ErrVersionConflict
	}
	if err := scannerrelease.ValidateRolloutTransition(s.rollout.State, target); err != nil {
		return nil, err
	}
	s.rollout.State = target
	s.rollout.Version++
	copy := s.rollout
	return &copy, nil
}

func (s *controllerStore) ListRolloutCohorts(
	_ context.Context,
	rolloutID string,
) ([]scannerrelease.RolloutCohort, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rolloutID != s.rollout.ID {
		return nil, errors.New("rollout not found")
	}
	return append([]scannerrelease.RolloutCohort(nil), s.cohorts...), nil
}

func (s *controllerStore) UpdateRolloutCohort(
	_ context.Context,
	cohort *scannerrelease.RolloutCohort,
	expected int64,
	_ scannerrelease.TransitionCommand,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failObservingOnce &&
		(cohort.State == CohortObserving || cohort.State == CohortRollbackObserving) {
		s.failObservingOnce = false
		return errors.New("injected cohort persistence failure")
	}
	for index := range s.cohorts {
		if s.cohorts[index].ID != cohort.ID {
			continue
		}
		if s.cohorts[index].Version != expected {
			return scannerrelease.ErrVersionConflict
		}
		cohort.Version = expected + 1
		s.cohorts[index] = *cohort
		return nil
	}
	return errors.New("cohort not found")
}

func (s *controllerStore) GetRelease(
	_ context.Context,
	id string,
) (*scannerrelease.Release, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	release, ok := s.releases[id]
	if !ok {
		return nil, errors.New("release not found")
	}
	copy := release
	return &copy, nil
}

func (s *controllerStore) rolloutSnapshot() scannerrelease.Rollout {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rollout
}

func (s *controllerStore) cohortSnapshot() []scannerrelease.RolloutCohort {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]scannerrelease.RolloutCohort(nil), s.cohorts...)
}

func (s *controllerStore) activeClaim() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claim != nil && s.claim.State == scannerrelease.RolloutClaimActive
}

func (s *controllerStore) claimSnapshot() scannerrelease.RolloutClaim {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claim == nil {
		return scannerrelease.RolloutClaim{}
	}
	return *s.claim
}

func controllerEligible(state scannerrelease.RolloutState) bool {
	switch state {
	case scannerrelease.RolloutPending, scannerrelease.RolloutPreparing,
		scannerrelease.RolloutCanary, scannerrelease.RolloutVerifying,
		scannerrelease.RolloutRollingOut, scannerrelease.RolloutRollingBack,
		scannerrelease.RolloutPaused:
		return true
	default:
		return false
	}
}

type controllerRuntime struct {
	mu             sync.Mutex
	clock          *controllerClock
	assignments    []AssignmentRequest
	healthRequests []HealthRequest
	health         func(HealthRequest, time.Time) HealthSnapshot
	started        chan struct{}
	block          bool
	cancelled      bool
	startOnce      sync.Once
}

type controllerLifecycleRuntime struct {
	*controllerRuntime
	lifecycleMu sync.Mutex
	lifecycle   map[string][]AssignmentRequest
}

func (r *controllerLifecycleRuntime) Pause(
	_ context.Context,
	request AssignmentRequest,
) error {
	r.recordLifecycle("pause", request)
	return nil
}

func (r *controllerLifecycleRuntime) Resume(
	_ context.Context,
	request AssignmentRequest,
) error {
	r.recordLifecycle("resume", request)
	return nil
}

func (r *controllerLifecycleRuntime) Cancel(
	_ context.Context,
	request AssignmentRequest,
) error {
	r.recordLifecycle("cancel", request)
	return nil
}

func (r *controllerLifecycleRuntime) recordLifecycle(
	action string,
	request AssignmentRequest,
) {
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if r.lifecycle == nil {
		r.lifecycle = make(map[string][]AssignmentRequest)
	}
	r.lifecycle[action] = append(r.lifecycle[action], request)
}

func (r *controllerLifecycleRuntime) lifecycleSnapshot(
	action string,
) []AssignmentRequest {
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	return append([]AssignmentRequest(nil), r.lifecycle[action]...)
}

func (r *controllerRuntime) Assign(ctx context.Context, request AssignmentRequest) error {
	r.mu.Lock()
	r.assignments = append(r.assignments, request)
	r.mu.Unlock()
	if r.started != nil {
		r.startOnce.Do(func() { close(r.started) })
	}
	if !r.block {
		return nil
	}
	<-ctx.Done()
	r.mu.Lock()
	r.cancelled = true
	r.mu.Unlock()
	return ctx.Err()
}

func (r *controllerRuntime) Health(
	_ context.Context,
	request HealthRequest,
) (HealthSnapshot, error) {
	now := r.clock.Now()
	r.mu.Lock()
	r.healthRequests = append(r.healthRequests, request)
	r.mu.Unlock()
	if r.health != nil {
		return r.health(request, now), nil
	}
	return healthySnapshot(request.DesiredReleaseID, now), nil
}

func (r *controllerRuntime) healthSnapshot() []HealthRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]HealthRequest(nil), r.healthRequests...)
}

func (r *controllerRuntime) assignmentSnapshot() []AssignmentRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]AssignmentRequest(nil), r.assignments...)
}

func (r *controllerRuntime) wasCancelled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cancelled
}

type fakeWorkerStatusStore struct {
	mu       sync.Mutex
	statuses []scannerrelease.WorkerReleaseStatus
}

func (s *fakeWorkerStatusStore) AssignWorkerReleaseStatuses(
	_ context.Context,
	cohort, desiredReleaseID, operationID string,
	activeAfter, assignedAt time.Time,
) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var affected int64
	for index := range s.statuses {
		status := &s.statuses[index]
		if status.Cohort != cohort || status.LastHeartbeat.Before(activeAfter) ||
			status.AssignmentOperationID == operationID {
			continue
		}
		status.DesiredReleaseID = desiredReleaseID
		status.ObservedReleaseID = ""
		status.CachedDigestsJSON = "[]"
		status.VerificationState = "pending"
		status.VerificationError = ""
		status.CapabilitiesJSON = "{}"
		status.AssignmentOperationID = operationID
		status.AssignedAt = &assignedAt
		status.EvidenceObservedAt = nil
		status.Version++
		affected++
	}
	return affected, nil
}

func (s *fakeWorkerStatusStore) UpsertWorkerReleaseStatus(
	_ context.Context,
	status *scannerrelease.WorkerReleaseStatus,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.statuses {
		if s.statuses[index].WorkerID == status.WorkerID {
			s.statuses[index] = *status
			return nil
		}
	}
	s.statuses = append(s.statuses, *status)
	return nil
}

func (s *fakeWorkerStatusStore) ListWorkerReleaseStatuses(
	_ context.Context,
	cohort string,
	activeAfter time.Time,
) ([]scannerrelease.WorkerReleaseStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []scannerrelease.WorkerReleaseStatus
	for _, status := range s.statuses {
		if status.LastHeartbeat.Before(activeAfter) || (cohort != "" && status.Cohort != cohort) {
			continue
		}
		result = append(result, status)
	}
	return result, nil
}

func (s *fakeWorkerStatusStore) workerSnapshot(
	t *testing.T,
	workerID string,
) scannerrelease.WorkerReleaseStatus {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, status := range s.statuses {
		if status.WorkerID == workerID {
			return status
		}
	}
	t.Fatalf("worker %q not found", workerID)
	return scannerrelease.WorkerReleaseStatus{}
}
