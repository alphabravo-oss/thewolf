package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestScannerRegistryJobRepositoryContractSQLite(t *testing.T) {
	runScannerRegistryJobRepositoryContract(t, newSQLiteReleaseContractBackend)
}

func TestScannerRegistryJobRepositoryContractPostgres(t *testing.T) {
	runScannerRegistryJobRepositoryContract(t, newPostgresReleaseContractBackend)
}

func runScannerRegistryJobRepositoryContract(
	t *testing.T,
	factory func(*testing.T) releaseContractBackend,
) {
	t.Helper()
	backend := factory(t)
	t.Cleanup(func() { _ = backend.close() })
	ctx := context.Background()
	repository := backend.persistence
	registry := newRegistry()
	registry.Type = scannerrelease.RegistryMirror
	if err := repository.CreateRegistryTarget(ctx, registry); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	job := &scannerrelease.RegistryJob{
		RegistryTargetID: registry.ID, Kind: scannerrelease.RegistryJobCleanup,
		ReSignPolicy: scannerrelease.RegistryReSignForbidden,
		Actor:        "operator@example.test", Reason: "contract cleanup",
		IdempotencyKey: "registry-cleanup:" + uuid.NewString(), MaxAttempts: 2,
		AvailableAt: now,
	}
	if err := repository.CreateRegistryJob(ctx, job, command(job.IdempotencyKey)); err != nil {
		t.Fatalf("CreateRegistryJob: %v", err)
	}
	repeated := *job
	repeated.ID = uuid.NewString()
	if err := repository.CreateRegistryJob(ctx, &repeated, command(job.IdempotencyKey)); err != nil {
		t.Fatalf("idempotent CreateRegistryJob: %v", err)
	}
	if repeated.ID != job.ID {
		t.Fatalf("idempotent job ID = %s, want %s", repeated.ID, job.ID)
	}
	conflict := *job
	conflict.ID = uuid.NewString()
	conflict.Kind = scannerrelease.RegistryJobReconcile
	conflict.ReleaseID = "different-release"
	if err := repository.CreateRegistryJob(ctx, &conflict, command(job.IdempotencyKey)); !errors.Is(err, scannerrelease.ErrIdempotencyConflict) {
		t.Fatalf("conflicting idempotency error = %v", err)
	}

	claimed, err := repository.ClaimNextRegistryJob(ctx, "registry-a", now, now.Add(time.Minute))
	if err != nil || claimed == nil || claimed.Attempt != 1 || claimed.LeaseToken == "" {
		t.Fatalf("ClaimNextRegistryJob = %#v err=%v", claimed, err)
	}
	stale, err := repository.HeartbeatRegistryJob(
		ctx, claimed.ID, "registry-a", "wrong-token",
		now.Add(time.Second), now.Add(time.Minute),
	)
	if err != nil || stale.Current {
		t.Fatalf("stale registry heartbeat = %#v err=%v", stale, err)
	}
	current, err := repository.HeartbeatRegistryJob(
		ctx, claimed.ID, "registry-a", claimed.LeaseToken,
		now.Add(time.Second), now.Add(2*time.Minute),
	)
	if err != nil || !current.Current {
		t.Fatalf("current registry heartbeat = %#v err=%v", current, err)
	}
	retryAt := now.Add(30 * time.Second)
	retrying, err := repository.FinalizeRegistryJob(
		ctx, claimed.ID, "registry-a", claimed.LeaseToken,
		scannerrelease.RegistryJobRetry, retryAt, `{"partial":true}`,
		"registry_unavailable", "temporary", now.Add(2*time.Second),
	)
	if err != nil || retrying.State != scannerrelease.RegistryJobRetry {
		t.Fatalf("retry finalization = %#v err=%v", retrying, err)
	}
	early, err := repository.ClaimNextRegistryJob(
		ctx, "registry-b", retryAt.Add(-time.Second), retryAt.Add(time.Minute),
	)
	if err != nil || early != nil {
		t.Fatalf("early retry claim = %#v err=%v", early, err)
	}
	second, err := repository.ClaimNextRegistryJob(
		ctx, "registry-b", retryAt, retryAt.Add(time.Minute),
	)
	if err != nil || second == nil || second.Attempt != 2 {
		t.Fatalf("second claim = %#v err=%v", second, err)
	}
	reclaimed, err := repository.ReclaimStaleRegistryJobs(ctx, retryAt.Add(2*time.Minute))
	if err != nil || reclaimed.DeadLettered != 1 {
		t.Fatalf("ReclaimStaleRegistryJobs = %#v err=%v", reclaimed, err)
	}
	dead, err := repository.GetRegistryJob(ctx, job.ID)
	if err != nil || dead.State != scannerrelease.RegistryJobDeadLetter {
		t.Fatalf("dead-letter job = %#v err=%v", dead, err)
	}
	retried, err := repository.RetryDeadLetterRegistryJob(
		ctx, dead.ID, dead.Version, command("manual-retry:"+dead.ID),
		retryAt.Add(3*time.Minute),
	)
	if err != nil || retried.State != scannerrelease.RegistryJobRetry || retried.Attempt != 0 {
		t.Fatalf("manual retry = %#v err=%v", retried, err)
	}

	observation := &scannerrelease.RegistryImageObservation{
		JobID: job.ID, ImageKey: "default",
		DestinationReference: registry.Host + "/wolf/default@sha256:image",
		ExpectedDigest:       "sha256:image", DestinationDigest: "sha256:image",
		State: "matched", DetailJSON: `{"readback":true}`, CheckedAt: now,
	}
	if err := repository.UpsertRegistryImageObservation(ctx, observation); err != nil {
		t.Fatal(err)
	}
	observation.State = "repaired"
	observation.DestinationSBOMDigest = "sha256:sbom"
	if err := repository.UpsertRegistryImageObservation(ctx, observation); err != nil {
		t.Fatal(err)
	}
	observations, err := repository.ListRegistryImageObservations(ctx, job.ID)
	if err != nil || len(observations) != 1 || observations[0].State != "repaired" {
		t.Fatalf("registry observations = %#v err=%v", observations, err)
	}

	runQuarantineDeletionContract(t, ctx, repository, registry, now)
}

func runQuarantineDeletionContract(
	t *testing.T,
	ctx context.Context,
	repository scannerrelease.Persistence,
	registry *scannerrelease.RegistryTarget,
	now time.Time,
) {
	t.Helper()
	referencedDigest := "sha256:referenced-" + uuid.NewString()
	policy := newPolicy("registry-quarantine:"+uuid.NewString(), 1)
	if err := repository.CreatePolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	candidate := newCandidate(policy)
	if err := repository.CreateCandidate(ctx, candidate, command("candidate:"+candidate.ID)); err != nil {
		t.Fatal(err)
	}
	release := registryRelease(candidate, policy, registry, referencedDigest)
	if err := repository.CreateRelease(ctx, release, command("release:"+release.Release.ID)); err != nil {
		t.Fatal(err)
	}
	referenced := &scannerrelease.RegistryQuarantineObject{
		RegistryTargetID: registry.ID, CandidateID: candidate.ID,
		Repository: "wolf/default", Digest: referencedDigest, ObjectKind: "manifest",
		State: "orphaned", RetainUntil: timePointer(now.Add(-time.Hour)),
		DiscoveredAt: now.Add(-24 * time.Hour),
	}
	if err := repository.UpsertRegistryQuarantineObject(ctx, referenced); err != nil {
		t.Fatal(err)
	}
	_, decision, err := repository.AuthorizeRegistryQuarantineDeletion(
		ctx, referenced.ID, "cleanup-a", now, now.Add(time.Minute),
	)
	if err != nil || decision.Eligible {
		t.Fatalf("referenced object deletion decision = %#v err=%v", decision, err)
	}
	if !containsString(decision.Reasons, "release_image_reference") {
		t.Fatalf("referenced object reasons = %#v", decision.Reasons)
	}

	racingDigest := "sha256:racing-" + uuid.NewString()
	racing := &scannerrelease.RegistryQuarantineObject{
		RegistryTargetID: registry.ID, Repository: "wolf/racing",
		Digest: racingDigest, ObjectKind: "manifest", State: "orphaned",
		RetainUntil: timePointer(now.Add(-time.Hour)), DiscoveredAt: now.Add(-24 * time.Hour),
	}
	if err := repository.UpsertRegistryQuarantineObject(ctx, racing); err != nil {
		t.Fatal(err)
	}
	authorized, decision, err := repository.AuthorizeRegistryQuarantineDeletion(
		ctx, racing.ID, "cleanup-b", now, now.Add(time.Minute),
	)
	if err != nil || !decision.Eligible || authorized.DeletionLeaseToken == "" {
		t.Fatalf("unreferenced deletion decision = %#v object=%#v err=%v", decision, authorized, err)
	}
	secondCandidate := newCandidate(policy)
	if err := repository.CreateCandidate(ctx, secondCandidate, command("candidate:"+secondCandidate.ID)); err != nil {
		t.Fatal(err)
	}
	racingRelease := registryRelease(secondCandidate, policy, registry, racingDigest)
	if err := repository.CreateRelease(
		ctx, racingRelease, command("release:"+racingRelease.Release.ID),
	); !errors.Is(err, scannerrelease.ErrVersionConflict) {
		t.Fatalf("publication racing deletion error = %v, want version conflict", err)
	}
	if err := repository.CompleteRegistryQuarantineDeletion(
		ctx, racing.ID, "cleanup-b", authorized.DeletionLeaseToken,
		true, "", now.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
}

func registryRelease(
	candidate *scannerrelease.Candidate,
	policy *scannerrelease.Policy,
	registry *scannerrelease.RegistryTarget,
	digest string,
) *scannerrelease.ReleaseInventory {
	id := uuid.NewString()
	return &scannerrelease.ReleaseInventory{
		Release: scannerrelease.Release{
			ID: id, Name: "scanner-set-" + id, CandidateID: candidate.ID,
			LockDigest: "sha256:lock-" + id, ManifestDigest: "sha256:manifest-" + id,
			ManifestURI: "oci://release/" + id, State: scannerrelease.ReleasePublished,
			SignerIdentity: "signer@example.test", PolicyID: policy.ID,
			PolicyRevision: policy.Revision, DefinitionCommit: "0123456789abcdef",
			Protected: true, RollbackEligible: true,
		},
		Images: []scannerrelease.ReleaseImage{{
			ImageKey: "default", RegistryTargetID: registry.ID,
			Repository: "wolf/default", Digest: digest,
			SignatureStatus: "verified", ProvenanceDigest: "sha256:provenance",
			SBOMDigest: "sha256:sbom",
		}},
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
