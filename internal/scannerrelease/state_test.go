package scannerrelease

import (
	"errors"
	"testing"
)

func TestStateMachinesAcceptDocumentedHappyPaths(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{"discovery", func() error { return ValidateDiscoveryTransition(DiscoveryComparing, DiscoveryProposing) }},
		{"candidate", func() error { return ValidateCandidateTransition(CandidateTesting, CandidateSecurityReview) }},
		{"build", func() error { return ValidateBuildTransition(BuildRunning, BuildCompleted) }},
		{"release", func() error { return ValidateReleaseTransition(ReleaseCanary, ReleaseStable) }},
		{"rollout", func() error { return ValidateRolloutTransition(RolloutVerifying, RolloutRollingOut) }},
		{"rollback", func() error { return ValidateRolloutTransition(RolloutFailed, RolloutRollingBack) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); err != nil {
				t.Fatalf("documented transition rejected: %v", err)
			}
		})
	}
}

func TestStateMachinesRejectSkippingAndTerminalMutation(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{"discovery skip", func() error { return ValidateDiscoveryTransition(DiscoveryQueued, DiscoveryCompleted) }},
		{"candidate skip", func() error { return ValidateCandidateTransition(CandidateDraft, CandidatePublished) }},
		{"build terminal", func() error { return ValidateBuildTransition(BuildCompleted, BuildRunning) }},
		{"release reversal", func() error { return ValidateReleaseTransition(ReleaseStable, ReleaseCanary) }},
		{"rollout terminal", func() error { return ValidateRolloutTransition(RolloutCompleted, RolloutPreparing) }},
		{"same state", func() error { return ValidateRolloutTransition(RolloutCanary, RolloutCanary) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("error = %v, want ErrInvalidTransition", err)
			}
		})
	}
}

func TestEveryDeclaredTransitionIsAccepted(t *testing.T) {
	for from, destinations := range discoveryTransitions {
		for _, to := range destinations {
			if err := ValidateDiscoveryTransition(from, to); err != nil {
				t.Errorf("discovery %q -> %q: %v", from, to, err)
			}
		}
	}
	for from, destinations := range candidateTransitions {
		for _, to := range destinations {
			if err := ValidateCandidateTransition(from, to); err != nil {
				t.Errorf("candidate %q -> %q: %v", from, to, err)
			}
		}
	}
	for from, destinations := range buildTransitions {
		for _, to := range destinations {
			if err := ValidateBuildTransition(from, to); err != nil {
				t.Errorf("build %q -> %q: %v", from, to, err)
			}
		}
	}
	for from, destinations := range releaseTransitions {
		for _, to := range destinations {
			if err := ValidateReleaseTransition(from, to); err != nil {
				t.Errorf("release %q -> %q: %v", from, to, err)
			}
		}
	}
	for from, destinations := range rolloutTransitions {
		for _, to := range destinations {
			if err := ValidateRolloutTransition(from, to); err != nil {
				t.Errorf("rollout %q -> %q: %v", from, to, err)
			}
		}
	}
}

func TestTerminalStatesHaveNoOutgoingTransitions(t *testing.T) {
	if destinations := discoveryTransitions[DiscoveryCompleted]; len(destinations) != 0 {
		t.Errorf("completed discovery has outgoing transitions: %v", destinations)
	}
	if destinations := candidateTransitions[CandidatePublished]; len(destinations) != 0 {
		t.Errorf("published candidate has outgoing transitions: %v", destinations)
	}
	if destinations := buildTransitions[BuildCompleted]; len(destinations) != 0 {
		t.Errorf("completed build has outgoing transitions: %v", destinations)
	}
	if destinations := releaseTransitions[ReleaseRevoked]; len(destinations) != 0 {
		t.Errorf("revoked release has outgoing transitions: %v", destinations)
	}
	if destinations := rolloutTransitions[RolloutCompleted]; len(destinations) != 0 {
		t.Errorf("completed rollout has outgoing transitions: %v", destinations)
	}
	if destinations := rolloutTransitions[RolloutRolledBack]; len(destinations) != 0 {
		t.Errorf("rolled-back rollout has outgoing transitions: %v", destinations)
	}
}
