package scannerrelease

import "testing"

// These builders keep state-specific tests explicit. A fixture intentionally
// sets only the aggregate identity and state; repository contract tests fill
// the persistence fields required by each scenario.
func discoveryFixture(state DiscoveryState) DiscoveryRun {
	return DiscoveryRun{ID: "discovery-" + string(state), State: state}
}

func candidateFixture(state CandidateState) Candidate {
	return Candidate{ID: "candidate-" + string(state), State: state}
}

func buildFixture(state BuildState) BuildRun {
	return BuildRun{ID: "build-" + string(state), State: state}
}

func releaseFixture(state ReleaseState) Release {
	return Release{ID: "release-" + string(state), State: state}
}

func rolloutFixture(state RolloutState) Rollout {
	return Rollout{ID: "rollout-" + string(state), State: state}
}

func TestStateFixtureBuildersCoverEveryState(t *testing.T) {
	discoveryStates := []DiscoveryState{
		DiscoveryQueued, DiscoveryResolving, DiscoveryComparing,
		DiscoveryProposing, DiscoveryCompleted, DiscoveryFailed,
		DiscoveryCancelled,
	}
	for _, state := range discoveryStates {
		if fixture := discoveryFixture(state); fixture.ID == "" || fixture.State != state {
			t.Errorf("invalid discovery fixture for %q: %#v", state, fixture)
		}
	}

	candidateStates := []CandidateState{
		CandidateDraft, CandidateAwaitingDefinition, CandidateQueued,
		CandidateBuilding, CandidateTesting, CandidateSecurityReview,
		CandidateAwaitingApproval, CandidateApproved, CandidatePublishing,
		CandidatePublished, CandidateBlocked, CandidateRejected, CandidateFailed,
	}
	for _, state := range candidateStates {
		if fixture := candidateFixture(state); fixture.ID == "" || fixture.State != state {
			t.Errorf("invalid candidate fixture for %q: %#v", state, fixture)
		}
	}

	buildStates := []BuildState{
		BuildQueued, BuildClaimed, BuildRunning, BuildCompleted,
		BuildFailed, BuildCancelled,
	}
	for _, state := range buildStates {
		if fixture := buildFixture(state); fixture.ID == "" || fixture.State != state {
			t.Errorf("invalid build fixture for %q: %#v", state, fixture)
		}
	}

	releaseStates := []ReleaseState{
		ReleasePublished, ReleaseCandidateChannel, ReleaseCanary,
		ReleaseStable, ReleaseDeprecated, ReleaseRevoked,
	}
	for _, state := range releaseStates {
		if fixture := releaseFixture(state); fixture.ID == "" || fixture.State != state {
			t.Errorf("invalid release fixture for %q: %#v", state, fixture)
		}
	}

	rolloutStates := []RolloutState{
		RolloutPending, RolloutPreparing, RolloutCanary, RolloutVerifying,
		RolloutRollingOut, RolloutCompleted, RolloutFailed, RolloutPaused,
		RolloutRollingBack, RolloutRolledBack,
	}
	for _, state := range rolloutStates {
		if fixture := rolloutFixture(state); fixture.ID == "" || fixture.State != state {
			t.Errorf("invalid rollout fixture for %q: %#v", state, fixture)
		}
	}
}
