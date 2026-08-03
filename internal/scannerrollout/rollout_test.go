package scannerrollout

import (
	"strings"
	"testing"
	"time"
)

func TestRolloutStateMachinePauseResumeAndRollback(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)
	rollout := Rollout{
		ID: "rollout-1", TargetID: "prod", FromReleaseID: "old", ToReleaseID: "new",
		State: StatePending, Version: 0, StartedAt: now, UpdatedAt: now,
	}
	var err error
	rollout, _, err = rollout.Transition(0, StatePreparing, "operator", "preflight passed", now)
	if err != nil {
		t.Fatal(err)
	}
	rollout, event, err := rollout.Transition(1, StatePaused, "operator", "maintenance pause", now)
	if err != nil {
		t.Fatal(err)
	}
	if rollout.ResumeState != StatePreparing || event.PreviousState != StatePreparing {
		t.Fatalf("paused rollout = %#v, event = %#v", rollout, event)
	}
	if _, _, err := rollout.Transition(2, StateCanary, "operator", "invalid resume", now); err == nil {
		t.Fatal("paused rollout resumed to the wrong state")
	}
	rollout, _, err = rollout.Transition(2, StatePreparing, "operator", "resume", now)
	if err != nil {
		t.Fatal(err)
	}
	rollout, _, err = rollout.Transition(3, StateCanary, "controller", "canary ready", now)
	if err != nil {
		t.Fatal(err)
	}
	rollout, _, err = rollout.Transition(4, StateRollingBack, "controller", "canary unhealthy", now)
	if err != nil {
		t.Fatal(err)
	}
	rollout, _, err = rollout.Transition(5, StateRolledBack, "controller", "prior release restored", now)
	if err != nil {
		t.Fatal(err)
	}
	if rollout.State != StateRolledBack {
		t.Fatalf("state = %s", rollout.State)
	}
	if _, _, err := rollout.Transition(6, StatePreparing, "operator", "illegal", now); err == nil {
		t.Fatal("terminal rollout accepted another transition")
	}
}

func TestRolloutRejectsStaleVersion(t *testing.T) {
	t.Parallel()
	rollout := Rollout{
		ID: "rollout-1", TargetID: "prod", FromReleaseID: "old", ToReleaseID: "new",
		State: StatePending, Version: 4,
	}
	if _, _, err := rollout.Transition(3, StatePreparing, "operator", "go", time.Now()); err == nil ||
		!strings.Contains(err.Error(), "stale rollout version") {
		t.Fatalf("stale transition error = %v", err)
	}
}

func TestCanaryMustMeetObservationAndSampleMinimum(t *testing.T) {
	t.Parallel()
	policy := DefaultCanaryPolicy()
	started := time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)
	decision, err := EvaluateCanary(policy, CanaryHealth{Samples: 2}, started, started.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != CanaryPending || len(decision.Reasons) != 2 {
		t.Fatalf("decision = %#v", decision)
	}
	decision, err = EvaluateCanary(
		policy,
		CanaryHealth{Samples: 20, StableSamples: 20},
		started,
		started.Add(20*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != CanaryPassed {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestCanaryHardFailuresTriggerRollbackImmediately(t *testing.T) {
	t.Parallel()
	policy := DefaultCanaryPolicy()
	started := time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)
	decision, err := EvaluateCanary(
		policy,
		CanaryHealth{
			Samples:               1,
			SignatureFailures:     1,
			ExpectedFindingLosses: 1,
		},
		started,
		started.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != CanaryRollback ||
		!containsReason(decision.Reasons, "signature") ||
		!containsReason(decision.Reasons, "findings") {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestCanaryDetectsRelativeFailureAndDurationRegression(t *testing.T) {
	t.Parallel()
	policy := DefaultCanaryPolicy()
	policy.MaxInfrastructureFailureRate = 0.50
	started := time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)
	decision, err := EvaluateCanary(
		policy,
		CanaryHealth{
			Samples:                      100,
			InfrastructureFailures:       4,
			StableSamples:                100,
			StableInfrastructureFailures: 1,
			CandidateP95Duration:         130 * time.Second,
			StableP95Duration:            100 * time.Second,
		},
		started,
		started.Add(20*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != CanaryRollback ||
		!containsReason(decision.Reasons, "against stable") ||
		!containsReason(decision.Reasons, "duration") {
		t.Fatalf("decision = %#v", decision)
	}
}

func containsReason(reasons []string, substring string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, substring) {
			return true
		}
	}
	return false
}
