package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestRolloutClaimLeaseTakeoverCooldownAndCohortTimestamps(t *testing.T) {
	t.Parallel()
	store, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repository := store.ScannerReleases()
	ctx := context.Background()

	oldRelease := createRolloutClaimRelease(t, ctx, repository, "old", scannerrelease.ReleaseStable)
	newRelease := createRolloutClaimRelease(t, ctx, repository, "new", scannerrelease.ReleasePublished)
	rollout := &scannerrelease.Rollout{
		ID: uuid.NewString(), Target: "production",
		FromReleaseID: oldRelease.ID, ToReleaseID: newRelease.ID,
		Strategy: "canary_then_stable", State: scannerrelease.RolloutPending,
		PolicySnapshotJSON: `{"schema_version":"wolf.scanner-policy/v1"}`,
		Actor:              "operator", IdempotencyKey: "rollout-claim-test",
	}
	if err := repository.CreateRollout(
		ctx, rollout,
		[]scannerrelease.RolloutCohort{{Name: "canary", Ordinal: 0}},
		rolloutClaimCommand("create-rollout"),
	); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	first, err := repository.ClaimNextRollout(ctx, "controller-a", now, now.Add(time.Minute))
	if err != nil || first == nil || first.Attempt != 1 {
		t.Fatalf("first claim = %#v, err = %v", first, err)
	}
	if duplicate, err := repository.ClaimNextRollout(
		ctx, "controller-b", now.Add(time.Second), now.Add(time.Minute),
	); err != nil || duplicate != nil {
		t.Fatalf("concurrent claim = %#v, err = %v", duplicate, err)
	}
	status, err := repository.HeartbeatRollout(
		ctx, rollout.ID, "controller-a", "wrong-token",
		now.Add(2*time.Second), now.Add(2*time.Minute),
	)
	if err != nil || status.Current {
		t.Fatalf("wrong-token heartbeat = %#v, err = %v", status, err)
	}

	takeoverAt := now.Add(time.Minute)
	second, err := repository.ClaimNextRollout(
		ctx, "controller-b", takeoverAt, takeoverAt.Add(time.Minute),
	)
	if err != nil || second == nil || second.Attempt != 2 ||
		second.LeaseToken == first.LeaseToken {
		t.Fatalf("stale takeover = %#v, err = %v", second, err)
	}
	if released, err := repository.ReleaseRolloutClaim(
		ctx, rollout.ID, "controller-a", first.LeaseToken,
		takeoverAt.Add(time.Second), takeoverAt.Add(time.Minute),
		rolloutClaimCommand("stale-release"),
	); err != nil || released {
		t.Fatalf("stale release = %t, err = %v", released, err)
	}

	releaseAt := takeoverAt.Add(2 * time.Second)
	availableAt := releaseAt.Add(30 * time.Second)
	if released, err := repository.ReleaseRolloutClaim(
		ctx, rollout.ID, "controller-b", second.LeaseToken,
		releaseAt, availableAt, rolloutClaimCommand("release-current"),
	); err != nil || !released {
		t.Fatalf("current release = %t, err = %v", released, err)
	}
	if early, err := repository.ClaimNextRollout(
		ctx, "controller-c", releaseAt.Add(time.Second), releaseAt.Add(time.Minute),
	); err != nil || early != nil {
		t.Fatalf("cooldown claim = %#v, err = %v", early, err)
	}
	third, err := repository.ClaimNextRollout(
		ctx, "controller-c", availableAt, availableAt.Add(time.Minute),
	)
	if err != nil || third == nil || third.Attempt != 3 {
		t.Fatalf("post-cooldown claim = %#v, err = %v", third, err)
	}

	cohorts, err := repository.ListRolloutCohorts(ctx, rollout.ID)
	if err != nil || len(cohorts) != 1 {
		t.Fatalf("cohorts = %#v, err = %v", cohorts, err)
	}
	started := now.Add(3 * time.Minute)
	observed := started.Add(time.Minute)
	completed := observed.Add(time.Minute)
	cohorts[0].StartedAt = &started
	cohorts[0].HealthObservedAt = &observed
	cohorts[0].CompletedAt = &completed
	if err := repository.UpdateRolloutCohort(
		ctx, &cohorts[0], cohorts[0].Version, rolloutClaimCommand("cohort-timestamps"),
	); err != nil {
		t.Fatal(err)
	}
	cohorts, err = repository.ListRolloutCohorts(ctx, rollout.ID)
	if err != nil || cohorts[0].StartedAt == nil || cohorts[0].HealthObservedAt == nil ||
		cohorts[0].CompletedAt == nil {
		t.Fatalf("persisted cohort timestamps = %#v, err = %v", cohorts, err)
	}

	events, err := repository.ListEvents(ctx, "rollout", rollout.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	foundReclaim := false
	for _, event := range events {
		if event.EventType == "rollout.reclaimed" {
			foundReclaim = true
		}
	}
	if !foundReclaim {
		t.Fatalf("rollout events have no stale reclaim: %#v", events)
	}
}

func createRolloutClaimRelease(
	t *testing.T,
	ctx context.Context,
	repository scannerrelease.Persistence,
	name string,
	state scannerrelease.ReleaseState,
) scannerrelease.Release {
	t.Helper()
	policy := &scannerrelease.Policy{
		ID: uuid.NewString(), Scope: "rollout-claim-" + uuid.NewString(), Revision: 1,
		Enabled: true, ScheduleJSON: `{}`, RulesJSON: `{}`, CreatedBy: "test",
	}
	if err := repository.CreatePolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	candidate := &scannerrelease.Candidate{
		ID: uuid.NewString(), DefinitionCommit: "commit", LockDigest: "sha256:lock",
		RiskSummaryJSON: `{}`, State: scannerrelease.CandidatePublished,
		RequiredGatesJSON: `[]`, PolicyID: policy.ID, PolicyRevision: policy.Revision,
		Actor: "test", IdempotencyKey: "candidate-" + uuid.NewString(),
	}
	if err := repository.CreateCandidate(
		ctx, candidate, rolloutClaimCommand("create-candidate-"+candidate.ID),
	); err != nil {
		t.Fatal(err)
	}
	inventory := &scannerrelease.ReleaseInventory{Release: scannerrelease.Release{
		ID: uuid.NewString(), Name: name, CandidateID: candidate.ID,
		LockDigest: "sha256:lock", ManifestDigest: "sha256:manifest-" + name,
		ManifestURI: "oci://example/" + name, State: state, SignerIdentity: "test",
		PolicyID: policy.ID, PolicyRevision: policy.Revision, DefinitionCommit: "commit",
		Protected: true, RollbackEligible: true,
	}}
	if err := repository.CreateRelease(
		ctx, inventory, rolloutClaimCommand("create-release-"+inventory.Release.ID),
	); err != nil {
		t.Fatal(err)
	}
	return inventory.Release
}

func rolloutClaimCommand(key string) scannerrelease.TransitionCommand {
	return scannerrelease.TransitionCommand{
		Actor: "test", Reason: key, IdempotencyKey: key, PayloadJSON: `{}`,
	}
}
