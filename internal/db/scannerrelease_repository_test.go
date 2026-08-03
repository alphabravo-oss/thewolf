package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannertrace"
)

type releaseContractBackend struct {
	persistence scannerrelease.Persistence
	exec        func(context.Context, string, ...any) (sql.Result, error)
	get         func(context.Context, any, string, ...any) error
	close       func() error
}

func sameOptionalCounter(before int, beforeErr error, after int, afterErr error) bool {
	if errors.Is(beforeErr, sql.ErrNoRows) && errors.Is(afterErr, sql.ErrNoRows) {
		return true
	}
	return beforeErr == nil && afterErr == nil && before == after
}

func newSQLiteReleaseContractBackend(t *testing.T) releaseContractBackend {
	t.Helper()
	store, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	return releaseContractBackend{
		persistence: store.ScannerReleases(),
		exec: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return store.db.ExecContext(ctx, query, args...)
		},
		get: func(ctx context.Context, destination any, query string, args ...any) error {
			return store.db.GetContext(ctx, destination, query, args...)
		},
		close: store.Close,
	}
}

func newPostgresReleaseContractBackend(t *testing.T) releaseContractBackend {
	t.Helper()
	dsn := os.Getenv("WOLF_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WOLF_TEST_POSTGRES_DSN is not configured")
	}
	admin, err := NewPostgres(dsn)
	if err != nil {
		t.Fatalf("NewPostgres admin: %v", err)
	}
	schema := "release_contract_" + uuid.NewString()
	if _, err := admin.db.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		_ = admin.Close()
		t.Fatalf("create isolated PostgreSQL schema: %v", err)
	}
	store, err := NewPostgres(postgresDSNWithSearchPath(t, dsn, schema))
	if err != nil {
		_, _ = admin.db.Exec(`DROP SCHEMA "` + schema + `" CASCADE`)
		_ = admin.Close()
		t.Fatalf("NewPostgres isolated schema: %v", err)
	}
	return releaseContractBackend{
		persistence: store.ScannerReleases(),
		exec: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return store.db.ExecContext(ctx, store.db.Rebind(query), args...)
		},
		get: func(ctx context.Context, destination any, query string, args ...any) error {
			return store.db.GetContext(ctx, destination, store.db.Rebind(query), args...)
		},
		close: func() error {
			storeErr := store.Close()
			_, dropErr := admin.db.Exec(`DROP SCHEMA "` + schema + `" CASCADE`)
			adminErr := admin.Close()
			return errors.Join(storeErr, dropErr, adminErr)
		},
	}
}

func TestScannerReleaseRepositoryContractSQLite(t *testing.T) {
	runScannerReleaseRepositoryContract(t, newSQLiteReleaseContractBackend)
}

func TestScannerReleaseRepositoryContractPostgres(t *testing.T) {
	runScannerReleaseRepositoryContract(t, newPostgresReleaseContractBackend)
}

func runScannerReleaseRepositoryContract(t *testing.T, factory func(*testing.T) releaseContractBackend) {
	t.Helper()
	t.Run("policy revisions and registry optimistic concurrency", func(t *testing.T) {
		backend := factory(t)
		t.Cleanup(func() { _ = backend.close() })
		ctx := context.Background()
		repository := backend.persistence
		scope := "organization:" + uuid.NewString()
		first := newPolicy(scope, 1)
		if err := repository.CreatePolicy(ctx, first); err != nil {
			t.Fatalf("CreatePolicy(first): %v", err)
		}
		second := newPolicy(scope, 2)
		if err := repository.CreatePolicy(ctx, second); err != nil {
			t.Fatalf("CreatePolicy(second): %v", err)
		}
		active, err := repository.ListPolicies(ctx, scope, true)
		if err != nil {
			t.Fatalf("ListPolicies: %v", err)
		}
		if len(active) != 1 || active[0].ID != second.ID {
			t.Fatalf("active policies = %#v, want only revision two", active)
		}
		old, err := repository.GetPolicy(ctx, first.ID)
		if err != nil || old.Enabled {
			t.Fatalf("prior revision should be disabled: policy=%#v err=%v", old, err)
		}

		registry := newRegistry()
		if err := repository.CreateRegistryTarget(ctx, registry); err != nil {
			t.Fatalf("CreateRegistryTarget: %v", err)
		}
		registry.Namespace = "new-namespace"
		if err := repository.UpdateRegistryTarget(ctx, registry, 99); !errors.Is(err, scannerrelease.ErrVersionConflict) {
			t.Fatalf("stale registry update error = %v, want ErrVersionConflict", err)
		}
		if err := repository.UpdateRegistryTarget(ctx, registry, 1); err != nil {
			t.Fatalf("UpdateRegistryTarget: %v", err)
		}
		got, err := repository.GetRegistryTarget(ctx, registry.ID)
		if err != nil || got.Namespace != "new-namespace" || got.Version != 2 {
			t.Fatalf("registry round trip = %#v err=%v", got, err)
		}
	})

	t.Run("transition event atomicity idempotency and pagination", func(t *testing.T) {
		backend := factory(t)
		t.Cleanup(func() { _ = backend.close() })
		ctx := scannertrace.With(context.Background(), scannertrace.Correlation{
			TraceID:     "0123456789abcdef0123456789abcdef",
			OperationID: "operation-contract-123",
			Component:   "repository-test",
		})
		repository := backend.persistence
		policy := newPolicy("organization:"+uuid.NewString(), 1)
		if err := repository.CreatePolicy(ctx, policy); err != nil {
			t.Fatal(err)
		}
		run := newDiscovery(policy)
		createCommand := command("create:" + run.ID)
		if err := repository.CreateDiscoveryRun(ctx, run, createCommand); err != nil {
			t.Fatalf("CreateDiscoveryRun: %v", err)
		}
		retryCreate := newDiscovery(policy)
		retryCreate.IdempotencyKey = run.IdempotencyKey
		if err := repository.CreateDiscoveryRun(ctx, retryCreate, createCommand); err != nil {
			t.Fatalf("idempotent CreateDiscoveryRun: %v", err)
		}
		if retryCreate.ID != run.ID {
			t.Fatalf("idempotent create returned ID %s, want %s", retryCreate.ID, run.ID)
		}
		items := []scannerrelease.UpdateItem{
			{
				ComponentType:  scannerrelease.ComponentTool,
				ComponentName:  "semgrep",
				CurrentValue:   "1.0.0",
				AvailableValue: "1.0.1",
				RiskClass:      scannerrelease.RiskLow,
				SelectionState: "selected",
			},
			{
				ComponentType:  scannerrelease.ComponentBaseImage,
				ComponentName:  "debian",
				CurrentValue:   "sha256:old",
				AvailableValue: "sha256:new",
				RiskClass:      scannerrelease.RiskHigh,
			},
		}
		if err := repository.AddUpdateItems(ctx, run.ID, items); err != nil {
			t.Fatalf("AddUpdateItems: %v", err)
		}
		gotItems, err := repository.ListUpdateItems(ctx, run.ID)
		if err != nil || len(gotItems) != 2 {
			t.Fatalf("ListUpdateItems len=%d err=%v", len(gotItems), err)
		}
		run.AvailableCount = 2
		run.SelectedCount = 1
		summarized, err := repository.UpdateDiscoverySummary(
			ctx, run, 1, command("summary:"+run.ID),
		)
		if err != nil || summarized.Version != 2 || summarized.AvailableCount != 2 {
			t.Fatalf("UpdateDiscoverySummary = %#v err=%v", summarized, err)
		}

		transition := command("resolve:" + run.ID)
		resolving, err := repository.TransitionDiscovery(ctx, run.ID, 2, scannerrelease.DiscoveryResolving, transition)
		if err != nil {
			t.Fatalf("TransitionDiscovery: %v", err)
		}
		if resolving.Version != 3 {
			t.Fatalf("version = %d, want 3", resolving.Version)
		}
		// Retrying the exact command is successful and does not add an event,
		// even though the caller still holds the pre-command version.
		retried, err := repository.TransitionDiscovery(ctx, run.ID, 2, scannerrelease.DiscoveryResolving, transition)
		if err != nil || retried.Version != 3 {
			t.Fatalf("idempotent retry = %#v err=%v", retried, err)
		}
		if _, err := repository.TransitionDiscovery(
			ctx, run.ID, 3, scannerrelease.DiscoveryComparing, transition,
		); !errors.Is(err, scannerrelease.ErrIdempotencyConflict) {
			t.Fatalf("reused idempotency key error = %v, want ErrIdempotencyConflict", err)
		}
		if _, err := repository.TransitionDiscovery(
			ctx, run.ID, 2, scannerrelease.DiscoveryComparing, command("stale:"+run.ID),
		); !errors.Is(err, scannerrelease.ErrVersionConflict) {
			t.Fatalf("stale transition error = %v, want ErrVersionConflict", err)
		}
		if _, err := repository.TransitionDiscovery(
			ctx, run.ID, 3, scannerrelease.DiscoveryCompleted, command("skip:"+run.ID),
		); !errors.Is(err, scannerrelease.ErrInvalidTransition) {
			t.Fatalf("invalid transition error = %v, want ErrInvalidTransition", err)
		}
		events, err := repository.ListEvents(ctx, "discovery", run.ID, 0, 20)
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 3 || events[0].Sequence != 1 || events[1].Sequence != 2 || events[2].Sequence != 3 {
			t.Fatalf("events = %#v, want exactly ordered create+summary+transition", events)
		}
		for _, event := range events {
			if event.TraceID != "0123456789abcdef0123456789abcdef" ||
				event.OperationID != "operation-contract-123" {
				t.Fatalf("event lost durable operation correlation: %#v", event)
			}
		}
		correlation, err := repository.GetOperationCorrelation(
			ctx, "discovery", run.ID,
		)
		if err != nil || correlation.TraceID != events[0].TraceID ||
			correlation.OperationID != events[0].OperationID {
			t.Fatalf("operation correlation = %#v, err=%v", correlation, err)
		}
		filtered, err := repository.ListAllEvents(
			ctx,
			scannerrelease.EventFilter{
				TraceID:     "0123456789abcdef0123456789abcdef",
				OperationID: "operation-contract-123",
			},
			scannerrelease.PageRequest{Limit: 20},
		)
		if err != nil || len(filtered.Items) != len(events) {
			t.Fatalf("correlation-filtered events = %#v, err=%v", filtered, err)
		}
		if _, err := repository.ListAllEvents(
			ctx,
			scannerrelease.EventFilter{OperationID: "unsafe operation value"},
			scannerrelease.PageRequest{Limit: 20},
		); err == nil {
			t.Fatal("malformed operation filter was accepted")
		}

		for range 3 {
			other := newDiscovery(policy)
			if err := repository.CreateDiscoveryRun(ctx, other, command("create:"+other.ID)); err != nil {
				t.Fatal(err)
			}
		}
		firstPage, err := repository.ListDiscoveryRuns(
			ctx, scannerrelease.DiscoveryFilter{}, scannerrelease.PageRequest{Limit: 2},
		)
		if err != nil || len(firstPage.Items) != 2 || firstPage.NextCursor == "" {
			t.Fatalf("first page = %#v err=%v", firstPage, err)
		}
		secondPage, err := repository.ListDiscoveryRuns(
			ctx, scannerrelease.DiscoveryFilter{}, scannerrelease.PageRequest{Limit: 2, Cursor: firstPage.NextCursor},
		)
		if err != nil || len(secondPage.Items) != 2 {
			t.Fatalf("second page = %#v err=%v", secondPage, err)
		}
		seen := map[string]bool{}
		for _, item := range append(firstPage.Items, secondPage.Items...) {
			if seen[item.ID] {
				t.Fatalf("cursor pagination returned duplicate %s", item.ID)
			}
			seen[item.ID] = true
		}
	})

	t.Run("candidate build release rollout and append only evidence", func(t *testing.T) {
		backend := factory(t)
		t.Cleanup(func() { _ = backend.close() })
		ctx := context.Background()
		repository := backend.persistence
		policy := newPolicy("organization:"+uuid.NewString(), 1)
		if err := repository.CreatePolicy(ctx, policy); err != nil {
			t.Fatal(err)
		}
		registry := newRegistry()
		if err := repository.CreateRegistryTarget(ctx, registry); err != nil {
			t.Fatal(err)
		}
		candidate := newCandidate(policy)
		if err := repository.CreateCandidate(ctx, candidate, command("create:"+candidate.ID)); err != nil {
			t.Fatalf("CreateCandidate: %v", err)
		}
		candidate.ProposedCommit = "fedcba9876543210"
		candidate.ProposalURL = "https://git.example.test/pull/1"
		candidate.LockDigest = "sha256:proposed-lock"
		candidate.LockURI = "git://scanner-lock.yaml"
		proposed, err := repository.UpdateCandidateProposal(
			ctx, candidate, 1, command("proposal:"+candidate.ID),
		)
		if err != nil || proposed.Version != 2 || proposed.LockDigest != candidate.LockDigest {
			t.Fatalf("UpdateCandidateProposal = %#v err=%v", proposed, err)
		}
		queued, err := repository.TransitionCandidate(
			ctx, candidate.ID, 2, scannerrelease.CandidateQueued, command("queue:"+candidate.ID),
		)
		if err != nil || queued.Version != 3 {
			t.Fatalf("TransitionCandidate = %#v err=%v", queued, err)
		}
		candidatePage, err := repository.ListCandidates(
			ctx,
			scannerrelease.CandidateFilter{State: scannerrelease.CandidateQueued},
			scannerrelease.PageRequest{Limit: 10},
		)
		if err != nil || len(candidatePage.Items) != 1 || candidatePage.Items[0].ID != candidate.ID {
			t.Fatalf("filtered candidates = %#v err=%v", candidatePage, err)
		}

		build := &scannerrelease.BuildRun{
			ID:            uuid.NewString(),
			CandidateID:   candidate.ID,
			Attempt:       1,
			State:         scannerrelease.BuildQueued,
			PlatformsJSON: `["linux/amd64","linux/arm64"]`,
		}
		if err := repository.CreateBuildRun(ctx, build, command("create:"+build.ID)); err != nil {
			t.Fatalf("CreateBuildRun: %v", err)
		}
		step := &scannerrelease.BuildStep{
			ID:             uuid.NewString(),
			BuildRunID:     build.ID,
			StepKey:        "strict-smoke",
			State:          scannerrelease.BuildQueued,
			Attempt:        1,
			RetentionClass: "evidence",
		}
		if err := repository.CreateBuildStep(ctx, step, command("create:"+step.ID)); err != nil {
			t.Fatalf("CreateBuildStep: %v", err)
		}
		if _, err := repository.TransitionBuildStep(
			ctx, step.ID, 1, scannerrelease.BuildRunning, command("run:"+step.ID),
		); err != nil {
			t.Fatalf("TransitionBuildStep: %v", err)
		}
		step.OutputURI = "artifact://strict-smoke.log"
		step.OutputDigest = "sha256:smoke-log"
		step.SummaryJSON = `{"passed":true}`
		step.Protected = true
		recordedStep, err := repository.UpdateBuildStepEvidence(
			ctx, step, 2, command("evidence:"+step.ID),
		)
		if err != nil || recordedStep.Version != 3 || recordedStep.OutputDigest != step.OutputDigest {
			t.Fatalf("UpdateBuildStepEvidence = %#v err=%v", recordedStep, err)
		}
		steps, err := repository.ListBuildSteps(ctx, build.ID)
		if err != nil || len(steps) != 1 || steps[0].Version != 3 {
			t.Fatalf("ListBuildSteps = %#v err=%v", steps, err)
		}

		releaseID := uuid.NewString()
		inventory := &scannerrelease.ReleaseInventory{
			Release: scannerrelease.Release{
				ID:               releaseID,
				Name:             "scanner-set-" + releaseID,
				CandidateID:      candidate.ID,
				LockDigest:       "sha256:lock",
				ManifestDigest:   "sha256:manifest-" + releaseID,
				ManifestURI:      "oci://registry/release@" + releaseID,
				State:            scannerrelease.ReleasePublished,
				SignerIdentity:   "ci@example.test",
				PolicyID:         policy.ID,
				PolicyRevision:   policy.Revision,
				DefinitionCommit: "0123456789abcdef",
				Protected:        true,
				RollbackEligible: true,
			},
			Tools: []scannerrelease.ReleaseTool{{
				ToolKey:             "semgrep",
				Version:             "1.0.1",
				SourceReference:     "pypi:semgrep@1.0.1",
				Checksum:            "sha256:tool",
				ParserCompatibility: "v1",
			}},
			Images: []scannerrelease.ReleaseImage{{
				ImageKey:         "default",
				RegistryTargetID: registry.ID,
				Repository:       "wolf-scanners",
				Digest:           "sha256:image",
				PlatformDigests:  `{"linux/amd64":"sha256:amd64"}`,
				SignatureStatus:  "verified",
				ProvenanceDigest: "sha256:provenance",
				SBOMDigest:       "sha256:sbom",
			}, {
				ImageKey:         "fixer-codex",
				ImageKind:        scannerrelease.ReleaseImageFixer,
				RegistryTargetID: registry.ID,
				Repository:       "wolf-fixer-codex",
				Digest:           "sha256:fixer",
				PlatformDigests:  `{"linux/amd64":"sha256:fixer-amd64"}`,
				SignatureStatus:  "verified",
				ProvenanceDigest: "sha256:fixer-provenance",
				SBOMDigest:       "sha256:fixer-sbom",
			}},
			Artifacts: []scannerrelease.ReleaseArtifact{{
				ArtifactType:   "aggregate_sbom",
				MediaType:      "application/spdx+json",
				URI:            "oci://registry/sbom",
				Digest:         "sha256:aggregate-sbom",
				Protected:      true,
				RetentionClass: "release",
			}},
		}
		if err := repository.CreateRelease(ctx, inventory, command("publish:"+releaseID)); err != nil {
			t.Fatalf("CreateRelease: %v", err)
		}
		gotInventory, err := repository.GetReleaseInventory(ctx, releaseID)
		if err != nil {
			t.Fatalf("GetReleaseInventory: %v", err)
		}
		if len(gotInventory.Tools) != 1 || len(gotInventory.Images) != 2 ||
			len(gotInventory.Artifacts) != 1 || gotInventory.Release.ManifestDigest != inventory.Release.ManifestDigest {
			t.Fatalf("release inventory did not round-trip: %#v", gotInventory)
		}
		if gotInventory.Images[1].ImageKind != scannerrelease.ReleaseImageFixer {
			t.Fatalf("fixer image kind did not round-trip: %#v", gotInventory.Images)
		}
		approval := &scannerrelease.Approval{
			ID:             uuid.NewString(),
			CandidateID:    candidate.ID,
			Actor:          "approver@example.test",
			Action:         "exception",
			Reason:         "all mandatory gates passed",
			ExceptionScope: "vulnerability", ExceptionOwner: "security-owner",
			CompensatingControl: "quarantine candidate registry",
			EvidenceDigest:      "sha256:evidence",
			PolicyDecision:      "human_approval",
			IdempotencyKey:      "approval:" + candidate.ID,
		}
		if err := repository.AddApproval(ctx, approval); err != nil {
			t.Fatalf("AddApproval: %v", err)
		}
		if approvals, err := repository.ListApprovals(ctx, candidate.ID, ""); err != nil || len(approvals) != 1 ||
			approvals[0].ExceptionScope != "vulnerability" ||
			approvals[0].ExceptionOwner != "security-owner" ||
			approvals[0].CompensatingControl != "quarantine candidate registry" {
			t.Fatalf("ListApprovals = %#v err=%v", approvals, err)
		}

		if _, err := backend.exec(ctx,
			`UPDATE scanner_releases SET manifest_digest = ? WHERE id = ?`,
			"sha256:tampered", releaseID); err == nil {
			t.Fatal("database allowed immutable release identity mutation")
		}
		if _, err := backend.exec(ctx,
			`UPDATE scanner_release_artifacts SET digest = ? WHERE release_id = ?`,
			"sha256:tampered", releaseID); err == nil {
			t.Fatal("database allowed immutable evidence mutation")
		}
		if _, err := backend.exec(ctx,
			`DELETE FROM scanner_release_approvals WHERE id = ?`, approval.ID); err == nil {
			t.Fatal("database allowed approval deletion")
		}

		channelRelease, err := repository.TransitionRelease(
			ctx, releaseID, 1, scannerrelease.ReleaseCandidateChannel, command("candidate-channel:"+releaseID),
		)
		if err != nil || channelRelease.State != scannerrelease.ReleaseCandidateChannel {
			t.Fatalf("candidate channel transition = %#v err=%v", channelRelease, err)
		}
		release, err := repository.TransitionRelease(
			ctx, releaseID, 2, scannerrelease.ReleaseCanary, command("canary:"+releaseID),
		)
		if err != nil || release.State != scannerrelease.ReleaseCanary {
			t.Fatalf("mutable release state transition = %#v err=%v", release, err)
		}
		protected := true
		releasePage, err := repository.ListReleases(
			ctx,
			scannerrelease.ReleaseFilter{
				State:     scannerrelease.ReleaseCanary,
				Protected: &protected,
			},
			scannerrelease.PageRequest{Limit: 10},
		)
		if err != nil || len(releasePage.Items) != 1 || releasePage.Items[0].ID != releaseID {
			t.Fatalf("filtered releases = %#v err=%v", releasePage, err)
		}
		rollout := &scannerrelease.Rollout{
			ID:                 uuid.NewString(),
			Target:             "compose:" + uuid.NewString(),
			ToReleaseID:        releaseID,
			Strategy:           "canary_then_stable",
			PolicySnapshotJSON: `{"automaticRollback":true}`,
			Actor:              "operator@example.test",
		}
		cohorts := []scannerrelease.RolloutCohort{
			{Name: "canary", Ordinal: 0},
			{Name: "stable", Ordinal: 1},
		}
		if err := repository.CreateRollout(ctx, rollout, cohorts, command("create:"+rollout.ID)); err != nil {
			t.Fatalf("CreateRollout: %v", err)
		}
		if _, err := repository.TransitionRollout(
			ctx, rollout.ID, 1, scannerrelease.RolloutPreparing, command("prepare:"+rollout.ID),
		); err != nil {
			t.Fatalf("TransitionRollout: %v", err)
		}
		rolloutPage, err := repository.ListRollouts(
			ctx,
			scannerrelease.RolloutFilter{
				State:  scannerrelease.RolloutPreparing,
				Target: rollout.Target,
			},
			scannerrelease.PageRequest{Limit: 10},
		)
		if err != nil || len(rolloutPage.Items) != 1 || rolloutPage.Items[0].ID != rollout.ID {
			t.Fatalf("filtered rollouts = %#v err=%v", rolloutPage, err)
		}
		gotCohorts, err := repository.ListRolloutCohorts(ctx, rollout.ID)
		if err != nil || len(gotCohorts) != 2 {
			t.Fatalf("ListRolloutCohorts = %#v err=%v", gotCohorts, err)
		}
		gotCohorts[0].ObservedReleaseID = releaseID
		gotCohorts[0].State = "verified"
		gotCohorts[0].TotalWorkers = 1
		gotCohorts[0].ReadyWorkers = 1
		cohortCommand := command("cohort:" + gotCohorts[0].ID)
		if err := repository.UpdateRolloutCohort(ctx, &gotCohorts[0], 99, cohortCommand); !errors.Is(err, scannerrelease.ErrVersionConflict) {
			t.Fatalf("stale cohort error = %v, want ErrVersionConflict", err)
		}
		if err := repository.UpdateRolloutCohort(ctx, &gotCohorts[0], 1, cohortCommand); err != nil {
			t.Fatalf("UpdateRolloutCohort: %v", err)
		}

		oldAssignedAt := time.Now().UTC().Add(-2 * time.Minute)
		oldEvidenceAt := oldAssignedAt.Add(time.Minute)
		status := &scannerrelease.WorkerReleaseStatus{
			WorkerID:              "worker-" + uuid.NewString(),
			Cohort:                "canary",
			DesiredReleaseID:      releaseID,
			ObservedReleaseID:     releaseID,
			CachedDigestsJSON:     `["sha256:old"]`,
			VerificationState:     "verified",
			VerificationError:     "old verification detail",
			CapabilitiesJSON:      `{"samples":9}`,
			AssignmentOperationID: "old-assignment",
			AssignedAt:            &oldAssignedAt,
			EvidenceObservedAt:    &oldEvidenceAt,
		}
		if err := repository.UpsertWorkerReleaseStatus(ctx, status); err != nil {
			t.Fatalf("UpsertWorkerReleaseStatus: %v", err)
		}
		status.VerificationState = "drifted"
		if err := repository.UpsertWorkerReleaseStatus(ctx, status); err != nil {
			t.Fatalf("second UpsertWorkerReleaseStatus: %v", err)
		}
		statuses, err := repository.ListWorkerReleaseStatuses(ctx, "canary", time.Now().Add(-time.Minute))
		if err != nil || len(statuses) != 1 || statuses[0].Version != 2 {
			t.Fatalf("worker statuses = %#v err=%v", statuses, err)
		}

		assignedAt := time.Now().UTC()
		affected, err := repository.AssignWorkerReleaseStatuses(
			ctx, "canary", releaseID, "new-assignment",
			assignedAt.Add(-time.Minute), assignedAt,
		)
		if err != nil || affected != 1 {
			t.Fatalf("AssignWorkerReleaseStatuses = %d, %v", affected, err)
		}
		statuses, err = repository.ListWorkerReleaseStatuses(
			ctx, "canary", assignedAt.Add(-time.Minute),
		)
		if err != nil || len(statuses) != 1 {
			t.Fatalf("assigned worker statuses = %#v err=%v", statuses, err)
		}
		assigned := statuses[0]
		if assigned.AssignmentOperationID != "new-assignment" ||
			assigned.AssignedAt == nil || !assigned.AssignedAt.Equal(assignedAt) ||
			assigned.ObservedReleaseID != "" ||
			assigned.CachedDigestsJSON != "[]" ||
			assigned.VerificationState != "pending" ||
			assigned.VerificationError != "" ||
			assigned.CapabilitiesJSON != "{}" ||
			assigned.EvidenceObservedAt != nil ||
			assigned.Version != 3 {
			t.Fatalf("assignment did not invalidate prior evidence: %#v", assigned)
		}

		evidenceAt := assignedAt.Add(time.Second)
		assigned.ObservedReleaseID = releaseID
		assigned.VerificationState = "verified"
		assigned.CapabilitiesJSON = `{"samples":3}`
		assigned.EvidenceObservedAt = &evidenceAt
		assigned.LastHeartbeat = evidenceAt
		if err := repository.UpsertWorkerReleaseStatus(ctx, &assigned); err != nil {
			t.Fatalf("record post-assignment evidence: %v", err)
		}
		affected, err = repository.AssignWorkerReleaseStatuses(
			ctx, "canary", releaseID, "new-assignment",
			assignedAt.Add(-time.Minute), assignedAt.Add(2*time.Second),
		)
		if err != nil || affected != 0 {
			t.Fatalf("idempotent AssignWorkerReleaseStatuses = %d, %v", affected, err)
		}
		statuses, err = repository.ListWorkerReleaseStatuses(
			ctx, "canary", assignedAt.Add(-time.Minute),
		)
		if err != nil || len(statuses) != 1 ||
			statuses[0].ObservedReleaseID != releaseID ||
			statuses[0].EvidenceObservedAt == nil ||
			statuses[0].CapabilitiesJSON != `{"samples":3}` ||
			statuses[0].Version != 4 {
			t.Fatalf("idempotent assignment changed current evidence: %#v err=%v", statuses, err)
		}
	})

	t.Run("candidate publication is atomic recoverable and idempotent", func(t *testing.T) {
		backend := factory(t)
		t.Cleanup(func() { _ = backend.close() })
		ctx := context.Background()
		repository := backend.persistence
		policy := newPolicy("publication:"+uuid.NewString(), 1)
		if err := repository.CreatePolicy(ctx, policy); err != nil {
			t.Fatal(err)
		}
		candidate := newCandidate(policy)
		candidate.LockDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		candidate.PolicyDecision = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		if err := repository.CreateCandidate(ctx, candidate, command("atomic/create")); err != nil {
			t.Fatal(err)
		}
		version := candidate.Version
		for _, state := range []scannerrelease.CandidateState{
			scannerrelease.CandidateQueued,
			scannerrelease.CandidateBuilding,
			scannerrelease.CandidateTesting,
			scannerrelease.CandidateSecurityReview,
			scannerrelease.CandidateAwaitingApproval,
			scannerrelease.CandidateApproved,
		} {
			updated, err := repository.TransitionCandidate(
				ctx, candidate.ID, version, state,
				command("atomic/"+string(state)),
			)
			if err != nil {
				t.Fatalf("advance candidate to %s: %v", state, err)
			}
			version = updated.Version
		}
		releaseID := uuid.NewString()
		now := time.Now().UTC()
		inventory := &scannerrelease.ReleaseInventory{
			Release: scannerrelease.Release{
				ID:          releaseID,
				CandidateID: candidate.ID, LockDigest: candidate.LockDigest,
				ManifestDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
				ManifestURI:    "oci://registry/releases@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
				State:          scannerrelease.ReleasePublished, SignerIdentity: "release@example.test",
				PolicyID: policy.ID, PolicyRevision: policy.Revision,
				DefinitionCommit: candidate.DefinitionCommit,
				PublishedAt:      now, CreatedAt: now, UpdatedAt: now,
			},
			Tools: []scannerrelease.ReleaseTool{
				{ToolKey: "semgrep", Version: "1", SourceReference: "test", ParserCompatibility: "v1"},
				{ToolKey: "semgrep", Version: "1", SourceReference: "duplicate", ParserCompatibility: "v1"},
			},
		}
		year, week := now.ISOWeek()
		period := fmt.Sprintf("%04d.%02d", year, week)
		var counterBefore int
		counterBeforeErr := backend.get(ctx, &counterBefore,
			`SELECT next_sequence FROM scanner_release_sequence_counters WHERE period_key = ?`,
			period,
		)
		if _, err := repository.CommitCandidatePublication(
			ctx, candidate.ID, version, inventory, command("atomic/publish"),
		); err == nil {
			t.Fatal("publication with conflicting inventory unexpectedly succeeded")
		}
		var counterAfter int
		counterAfterErr := backend.get(ctx, &counterAfter,
			`SELECT next_sequence FROM scanner_release_sequence_counters WHERE period_key = ?`,
			period,
		)
		if !sameOptionalCounter(counterBefore, counterBeforeErr, counterAfter, counterAfterErr) {
			t.Fatalf(
				"failed publication consumed release sequence: before=(%d,%v) after=(%d,%v)",
				counterBefore, counterBeforeErr, counterAfter, counterAfterErr,
			)
		}
		unchanged, err := repository.GetCandidate(ctx, candidate.ID)
		if err != nil {
			t.Fatal(err)
		}
		if unchanged.State != scannerrelease.CandidateApproved || unchanged.Version != version {
			t.Fatalf("failed publication leaked candidate state: %#v", unchanged)
		}
		if _, err := repository.GetRelease(ctx, releaseID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("failed publication leaked release err=%v", err)
		}
		inventory.Tools = inventory.Tools[:1]
		published, err := repository.CommitCandidatePublication(
			ctx, candidate.ID, version, inventory, command("atomic/publish"),
		)
		if err != nil {
			t.Fatalf("retry atomic publication: %v", err)
		}
		if published.ID != releaseID {
			t.Fatalf("published release=%s want=%s", published.ID, releaseID)
		}
		if _, _, ok := parseScannerReleaseName(published.Name); !ok {
			t.Fatalf("published release has invalid allocated name %q", published.Name)
		}
		finalCandidate, err := repository.GetCandidate(ctx, candidate.ID)
		if err != nil || finalCandidate.State != scannerrelease.CandidatePublished {
			t.Fatalf("published candidate=%#v err=%v", finalCandidate, err)
		}
		replayed, err := repository.CommitCandidatePublication(
			ctx, candidate.ID, version, inventory, command("atomic/publish"),
		)
		if err != nil || replayed.ID != releaseID {
			t.Fatalf("idempotent replay=%#v err=%v", replayed, err)
		}

		nextCandidate := newCandidate(policy)
		nextCandidate.LockDigest = candidate.LockDigest
		nextCandidate.PolicyDecision = candidate.PolicyDecision
		if err := repository.CreateCandidate(ctx, nextCandidate, command("atomic/next/create")); err != nil {
			t.Fatal(err)
		}
		nextVersion := nextCandidate.Version
		for _, state := range []scannerrelease.CandidateState{
			scannerrelease.CandidateQueued,
			scannerrelease.CandidateBuilding,
			scannerrelease.CandidateTesting,
			scannerrelease.CandidateSecurityReview,
			scannerrelease.CandidateAwaitingApproval,
			scannerrelease.CandidateApproved,
		} {
			updated, err := repository.TransitionCandidate(
				ctx, nextCandidate.ID, nextVersion, state,
				command("atomic/next/"+string(state)),
			)
			if err != nil {
				t.Fatalf("advance next candidate to %s: %v", state, err)
			}
			nextVersion = updated.Version
		}
		nextManifestDigest := "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
		nextInventory := &scannerrelease.ReleaseInventory{
			Release: scannerrelease.Release{
				ID: uuid.NewString(), CandidateID: nextCandidate.ID,
				LockDigest: nextCandidate.LockDigest, ManifestDigest: nextManifestDigest,
				ManifestURI: "oci://registry/releases@" + nextManifestDigest,
				State:       scannerrelease.ReleasePublished, SignerIdentity: "release@example.test",
				PolicyID: policy.ID, PolicyRevision: policy.Revision,
				DefinitionCommit: nextCandidate.DefinitionCommit,
				PublishedAt:      now, CreatedAt: now, UpdatedAt: now,
			},
			Tools: []scannerrelease.ReleaseTool{{
				ToolKey: "semgrep", Version: "1", SourceReference: "test",
				ParserCompatibility: "v1",
			}},
		}
		nextPublished, err := repository.CommitCandidatePublication(
			ctx, nextCandidate.ID, nextVersion, nextInventory, command("atomic/next/publish"),
		)
		if err != nil {
			t.Fatalf("publish next scanner release sequence: %v", err)
		}
		firstPeriod, firstSequence, firstOK := parseScannerReleaseName(published.Name)
		nextPeriod, nextSequence, nextOK := parseScannerReleaseName(nextPublished.Name)
		if !firstOK || !nextOK || firstPeriod != nextPeriod || nextSequence != firstSequence+1 {
			t.Fatalf(
				"allocated release names are not consecutive: first=%q next=%q",
				published.Name, nextPublished.Name,
			)
		}
	})

	t.Run("discovery claims heartbeats finalization cancellation and recovery", func(t *testing.T) {
		backend := factory(t)
		t.Cleanup(func() { _ = backend.close() })
		ctx := context.Background()
		repository := backend.persistence
		// PostgreSQL contract subtests intentionally share one externally
		// supplied database. Terminalize discovery fixtures left by earlier
		// subtests so this queue contract controls the oldest claim.
		if _, err := backend.exec(ctx,
			`UPDATE scanner_discovery_runs
			 SET state = ?, completed_at = ?, worker_id = '', lease_token = '',
			     lease_expires_at = NULL, heartbeat_at = NULL
			 WHERE state IN (?, ?, ?, ?)`,
			scannerrelease.DiscoveryCancelled, time.Now().UTC(),
			scannerrelease.DiscoveryQueued, scannerrelease.DiscoveryResolving,
			scannerrelease.DiscoveryComparing, scannerrelease.DiscoveryProposing,
		); err != nil {
			t.Fatal(err)
		}
		policy := newPolicy("organization:"+uuid.NewString(), 1)
		if err := repository.CreatePolicy(ctx, policy); err != nil {
			t.Fatal(err)
		}
		run := newDiscovery(policy)
		run.ScopeJSON = `{"mode":"selected","tools":["semgrep"]}`
		run.MaxAttempts = 2
		if err := repository.CreateDiscoveryRun(ctx, run, command("create:"+run.ID)); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		claimed, err := repository.ClaimNextDiscoveryRun(
			ctx, "discovery-worker-a", now.Add(time.Minute),
		)
		if err != nil || claimed == nil || claimed.ID != run.ID ||
			claimed.State != scannerrelease.DiscoveryResolving ||
			claimed.LeaseToken == "" || claimed.Attempt != 1 || claimed.Version != 2 {
			t.Fatalf("discovery claim = %#v err=%v", claimed, err)
		}
		if status, err := repository.HeartbeatDiscoveryRun(
			ctx, run.ID, "discovery-worker-a", "wrong-token", now.Add(90*time.Second),
		); err != nil || status.Current {
			t.Fatalf("stale discovery heartbeat = %#v err=%v", status, err)
		}
		status, err := repository.HeartbeatDiscoveryRun(
			ctx, run.ID, "discovery-worker-a", claimed.LeaseToken, now.Add(90*time.Second),
		)
		if err != nil || !status.Current || status.CancelRequested || status.Version != 2 {
			t.Fatalf("current discovery heartbeat = %#v err=%v", status, err)
		}
		completedAt := now.Add(time.Second)
		claimed.State = scannerrelease.DiscoveryCompleted
		claimed.DefinitionDigest = "sha256:definition"
		claimed.LockDigest = "sha256:lock"
		claimed.Coverage = 0.5
		claimed.TotalCount = 2
		claimed.CoveredCount = 1
		claimed.CurrentCount = 1
		claimed.UnreachableCount = 1
		claimed.ErrorClass = "partial_coverage"
		claimed.ErrorDetail = "one source unavailable"
		claimed.CompletedAt = &completedAt
		checkedAt := now
		if _, err := repository.FinalizeDiscoveryRun(
			ctx, claimed, status.Version, "stale-token", nil,
			command("stale-finalize:"+run.ID),
		); !errors.Is(err, scannerrelease.ErrLeaseNotOwned) {
			t.Fatalf("stale discovery finalization error = %v, want ErrLeaseNotOwned", err)
		}
		finalized, err := repository.FinalizeDiscoveryRun(
			ctx, claimed, status.Version, claimed.LeaseToken,
			[]scannerrelease.UpdateItem{{
				ComponentType:      scannerrelease.ComponentTool,
				ComponentName:      "semgrep",
				CurrentValue:       "1.0.0",
				AvailableValue:     "1.0.1",
				AvailableDigest:    "sha256:item",
				Status:             "update_available",
				SourceEvidenceJSON: `{"reference":"v1.0.1"}`,
				RiskClass:          scannerrelease.RiskLow,
				CompatibilityJSON:  `{"risk_reasons":["patch"]}`,
				Resolver:           "manifest-latest",
				Attempts:           1,
				CheckedAt:          &checkedAt,
			}}, command("finalize:"+run.ID),
		)
		if err != nil || finalized.State != scannerrelease.DiscoveryCompleted ||
			finalized.Coverage != 0.5 || finalized.TotalCount != 2 ||
			finalized.WorkerID != "" || finalized.LeaseToken != "" ||
			finalized.Version != 3 {
			t.Fatalf("finalized discovery = %#v err=%v", finalized, err)
		}
		latest, err := repository.GetLatestCompletedDiscovery(
			ctx, run.DefinitionCommit, run.PolicyID, run.PolicyRevision, run.ScopeJSON,
		)
		if err != nil || latest != nil {
			t.Fatalf("partial discovery must not be candidate-eligible = %#v err=%v", latest, err)
		}
		if missing, err := repository.GetLatestCompletedDiscovery(
			ctx, run.DefinitionCommit, run.PolicyID, run.PolicyRevision,
			`{"mode":"complete"}`,
		); err != nil || missing != nil {
			t.Fatalf("mismatched-scope latest discovery = %#v err=%v", missing, err)
		}
		items, err := repository.ListUpdateItems(ctx, run.ID)
		if err != nil || len(items) != 1 ||
			items[0].Status != "update_available" ||
			items[0].AvailableDigest != "sha256:item" ||
			items[0].Resolver != "manifest-latest" {
			t.Fatalf("final discovery items = %#v err=%v", items, err)
		}

		queuedCancel := newDiscovery(policy)
		if err := repository.CreateDiscoveryRun(
			ctx, queuedCancel, command("create:"+queuedCancel.ID),
		); err != nil {
			t.Fatal(err)
		}
		requested, err := repository.RequestDiscoveryCancellation(
			ctx, queuedCancel.ID, command("cancel:"+queuedCancel.ID), now,
		)
		if err != nil || !requested {
			t.Fatalf("cancel queued discovery requested=%v err=%v", requested, err)
		}
		cancelled, err := repository.GetDiscoveryRun(ctx, queuedCancel.ID)
		if err != nil || cancelled.State != scannerrelease.DiscoveryCancelled ||
			cancelled.CompletedAt == nil {
			t.Fatalf("cancelled queued discovery = %#v err=%v", cancelled, err)
		}

		activeCancel := newDiscovery(policy)
		if err := repository.CreateDiscoveryRun(
			ctx, activeCancel, command("create:"+activeCancel.ID),
		); err != nil {
			t.Fatal(err)
		}
		active, err := repository.ClaimNextDiscoveryRun(
			ctx, "discovery-worker-b", now.Add(time.Minute),
		)
		if err != nil || active == nil || active.ID != activeCancel.ID {
			t.Fatalf("active cancellation claim = %#v err=%v", active, err)
		}
		if requested, err := repository.RequestDiscoveryCancellation(
			ctx, active.ID, command("cancel:"+active.ID), now,
		); err != nil || !requested {
			t.Fatalf("cancel active discovery requested=%v err=%v", requested, err)
		}
		cancelStatus, err := repository.HeartbeatDiscoveryRun(
			ctx, active.ID, "discovery-worker-b", active.LeaseToken, now.Add(90*time.Second),
		)
		if err != nil || !cancelStatus.Current || !cancelStatus.CancelRequested {
			t.Fatalf("active cancel heartbeat = %#v err=%v", cancelStatus, err)
		}
		active.State = scannerrelease.DiscoveryCompleted
		forcedCancelled, err := repository.FinalizeDiscoveryRun(
			ctx, active, cancelStatus.Version, active.LeaseToken, nil,
			command("finalize:"+active.ID),
		)
		if err != nil || forcedCancelled.State != scannerrelease.DiscoveryCancelled {
			t.Fatalf("forced cancelled discovery = %#v err=%v", forcedCancelled, err)
		}

		retry := newDiscovery(policy)
		retry.MaxAttempts = 2
		if err := repository.CreateDiscoveryRun(
			ctx, retry, command("create:"+retry.ID),
		); err != nil {
			t.Fatal(err)
		}
		firstClaim, err := repository.ClaimNextDiscoveryRun(
			ctx, "discovery-worker-c", now.Add(time.Minute),
		)
		if err != nil || firstClaim == nil || firstClaim.ID != retry.ID {
			t.Fatalf("retry first claim = %#v err=%v", firstClaim, err)
		}
		if count, err := repository.ReclaimStaleDiscoveryRuns(
			ctx, now.Add(2*time.Minute),
		); err != nil || count != 1 {
			t.Fatalf("first discovery reclaim count=%d err=%v", count, err)
		}
		requeued, err := repository.GetDiscoveryRun(ctx, retry.ID)
		if err != nil || requeued.State != scannerrelease.DiscoveryQueued ||
			requeued.Attempt != 1 || requeued.WorkerID != "" {
			t.Fatalf("requeued discovery = %#v err=%v", requeued, err)
		}
		secondClaim, err := repository.ClaimNextDiscoveryRun(
			ctx, "discovery-worker-d", time.Now().UTC().Add(time.Minute),
		)
		if err != nil || secondClaim == nil || secondClaim.ID != retry.ID ||
			secondClaim.Attempt != 2 {
			t.Fatalf("retry second claim = %#v err=%v", secondClaim, err)
		}
		if count, err := repository.ReclaimStaleDiscoveryRuns(
			ctx, time.Now().UTC().Add(2*time.Minute),
		); err != nil || count != 1 {
			t.Fatalf("exhausted discovery reclaim count=%d err=%v", count, err)
		}
		exhausted, err := repository.GetDiscoveryRun(ctx, retry.ID)
		if err != nil || exhausted.State != scannerrelease.DiscoveryFailed ||
			exhausted.ErrorClass != "worker_lost" || exhausted.CompletedAt == nil {
			t.Fatalf("exhausted discovery = %#v err=%v", exhausted, err)
		}
	})

	t.Run("build queue claims heartbeats cancellation and stale recovery", func(t *testing.T) {
		backend := factory(t)
		t.Cleanup(func() { _ = backend.close() })
		ctx := context.Background()
		repository := backend.persistence
		policy := newPolicy("organization:"+uuid.NewString(), 1)
		if err := repository.CreatePolicy(ctx, policy); err != nil {
			t.Fatal(err)
		}
		candidate := newCandidate(policy)
		if err := repository.CreateCandidate(ctx, candidate, command("create:"+candidate.ID)); err != nil {
			t.Fatal(err)
		}
		invalid := &scannerrelease.BuildRun{
			ID:            uuid.NewString(),
			CandidateID:   candidate.ID,
			Attempt:       99,
			PlatformsJSON: `{"not":"a platform matrix"}`,
		}
		if err := repository.CreateBuildRun(ctx, invalid, command("create:"+invalid.ID)); err == nil {
			t.Fatal("CreateBuildRun accepted invalid platform metadata")
		}
		created := time.Now().UTC().Add(-time.Minute)
		arm := &scannerrelease.BuildRun{
			ID:            uuid.NewString(),
			CandidateID:   candidate.ID,
			Attempt:       1,
			PlatformsJSON: `[{"Key":"default","Platforms":["linux/arm64"]}]`,
			CreatedAt:     created,
			UpdatedAt:     created,
		}
		amd := &scannerrelease.BuildRun{
			ID:            uuid.NewString(),
			CandidateID:   candidate.ID,
			Attempt:       2,
			PlatformsJSON: `["linux/amd64"]`,
			CreatedAt:     created.Add(time.Second),
			UpdatedAt:     created.Add(time.Second),
		}
		requeue := &scannerrelease.BuildRun{
			ID:            uuid.NewString(),
			CandidateID:   candidate.ID,
			Attempt:       3,
			PlatformsJSON: `["linux/amd64"]`,
			CreatedAt:     created.Add(2 * time.Second),
			UpdatedAt:     created.Add(2 * time.Second),
		}
		for _, run := range []*scannerrelease.BuildRun{arm, amd, requeue} {
			if err := repository.CreateBuildRun(ctx, run, command("create:"+run.ID)); err != nil {
				t.Fatalf("CreateBuildRun(%s): %v", run.ID, err)
			}
		}
		now := time.Now().UTC()
		claimedAMD, err := repository.ClaimNextBuildRun(
			ctx, "worker-amd", []string{"linux/amd64"}, now.Add(time.Minute),
		)
		if err != nil || claimedAMD == nil || claimedAMD.ID != amd.ID ||
			claimedAMD.State != scannerrelease.BuildClaimed || claimedAMD.LeaseToken == "" ||
			claimedAMD.Version != 2 {
			t.Fatalf("amd claim = %#v err=%v", claimedAMD, err)
		}
		if status, err := repository.HeartbeatBuildRun(
			ctx, amd.ID, "worker-amd", "stale-token", now.Add(90*time.Second),
		); err != nil || status.Current {
			t.Fatalf("stale heartbeat = %#v err=%v", status, err)
		}
		status, err := repository.HeartbeatBuildRun(
			ctx, amd.ID, "worker-amd", claimedAMD.LeaseToken, now.Add(90*time.Second),
		)
		if err != nil || !status.Current || status.CancelRequested || status.Version != 2 {
			t.Fatalf("current heartbeat = %#v err=%v", status, err)
		}
		cancelCommand := command("cancel:" + amd.ID)
		requested, err := repository.RequestBuildCancellation(ctx, amd.ID, cancelCommand, now)
		if err != nil || !requested {
			t.Fatalf("RequestBuildCancellation requested=%v err=%v", requested, err)
		}
		if requested, err := repository.RequestBuildCancellation(ctx, amd.ID, cancelCommand, now); err != nil || requested {
			t.Fatalf("idempotent cancellation requested=%v err=%v", requested, err)
		}
		status, err = repository.HeartbeatBuildRun(
			ctx, amd.ID, "worker-amd", claimedAMD.LeaseToken, now.Add(90*time.Second),
		)
		if err != nil || !status.Current || !status.CancelRequested || status.Version != 3 {
			t.Fatalf("cancelling heartbeat = %#v err=%v", status, err)
		}
		if n, err := repository.ReclaimStaleBuildRuns(ctx, now.Add(2*time.Minute)); err != nil || n != 1 {
			t.Fatalf("reclaim cancelled build n=%d err=%v", n, err)
		}
		cancelled, err := repository.GetBuildRun(ctx, amd.ID)
		if err != nil || cancelled.State != scannerrelease.BuildCancelled ||
			cancelled.WorkerID != "" || cancelled.LeaseToken != "" {
			t.Fatalf("cancelled reclaimed build = %#v err=%v", cancelled, err)
		}

		claimedARM, err := repository.ClaimNextBuildRun(
			ctx, "worker-arm", []string{"linux/arm64"}, now.Add(time.Minute),
		)
		if err != nil || claimedARM == nil || claimedARM.ID != arm.ID {
			t.Fatalf("arm claim = %#v err=%v", claimedARM, err)
		}
		running, err := repository.TransitionBuildRun(
			ctx, arm.ID, claimedARM.Version, scannerrelease.BuildRunning,
			command("run:"+arm.ID),
		)
		if err != nil || running.State != scannerrelease.BuildRunning {
			t.Fatalf("start claimed build = %#v err=%v", running, err)
		}
		if n, err := repository.ReclaimStaleBuildRuns(ctx, now.Add(2*time.Minute)); err != nil || n != 1 {
			t.Fatalf("reclaim running build n=%d err=%v", n, err)
		}
		recovered, err := repository.GetBuildRun(ctx, arm.ID)
		if err != nil || recovered.State != scannerrelease.BuildQueued ||
			recovered.ErrorClass != "" || recovered.CompletedAt != nil ||
			recovered.WorkerID != "" || recovered.LeaseToken != "" {
			t.Fatalf("requeued running build = %#v err=%v", recovered, err)
		}
		replacement, err := repository.ClaimNextBuildRun(
			ctx, "worker-arm-replacement", []string{"linux/arm64"}, now.Add(3*time.Minute),
		)
		if err != nil || replacement == nil || replacement.ID != arm.ID {
			t.Fatalf("replacement claim = %#v err=%v", replacement, err)
		}
		if _, err := repository.TransitionBuildRun(
			ctx, replacement.ID, replacement.Version, scannerrelease.BuildFailed,
			command("finish-replacement:"+replacement.ID),
		); err != nil {
			t.Fatalf("finish replacement claim: %v", err)
		}

		claimedRequeue, err := repository.ClaimNextBuildRun(
			ctx, "worker-amd-2", []string{"linux/amd64"}, now.Add(time.Minute),
		)
		if err != nil || claimedRequeue == nil || claimedRequeue.ID != requeue.ID {
			t.Fatalf("requeue claim = %#v err=%v", claimedRequeue, err)
		}
		if n, err := repository.ReclaimStaleBuildRuns(ctx, now.Add(2*time.Minute)); err != nil || n != 1 {
			t.Fatalf("reclaim unstarted build n=%d err=%v", n, err)
		}
		requeued, err := repository.GetBuildRun(ctx, requeue.ID)
		if err != nil || requeued.State != scannerrelease.BuildQueued ||
			requeued.WorkerID != "" || requeued.LeaseToken != "" {
			t.Fatalf("requeued build = %#v err=%v", requeued, err)
		}

		start := make(chan struct{})
		results := make(chan *scannerrelease.BuildRun, 2)
		errs := make(chan error, 2)
		var wait sync.WaitGroup
		for _, workerID := range []string{"racing-worker-a", "racing-worker-b"} {
			workerID := workerID
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				claimed, err := repository.ClaimNextBuildRun(
					ctx, workerID, []string{"linux/amd64"}, time.Now().UTC().Add(time.Minute),
				)
				results <- claimed
				errs <- err
			}()
		}
		close(start)
		wait.Wait()
		close(results)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("concurrent ClaimNextBuildRun: %v", err)
			}
		}
		claims := 0
		for claimed := range results {
			if claimed != nil {
				claims++
				if claimed.ID != requeue.ID {
					t.Fatalf("concurrent claim selected unexpected build %s", claimed.ID)
				}
			}
		}
		if claims != 1 {
			t.Fatalf("concurrent claim count = %d, want exactly one", claims)
		}
	})

	t.Run("proposal claims heartbeat finalization release and stale recovery", func(t *testing.T) {
		backend := factory(t)
		t.Cleanup(func() { _ = backend.close() })
		ctx := context.Background()
		repository := backend.persistence
		if _, err := backend.exec(ctx,
			`UPDATE scanner_release_candidates
			 SET state = ?, proposal_worker_id = '', proposal_lease_token = '',
			     proposal_lease_expires_at = NULL, proposal_heartbeat_at = NULL
			 WHERE state = ?`,
			scannerrelease.CandidateFailed,
			scannerrelease.CandidateAwaitingDefinition,
		); err != nil {
			t.Fatal(err)
		}
		policy := newPolicy("organization:"+uuid.NewString(), 1)
		if err := repository.CreatePolicy(ctx, policy); err != nil {
			t.Fatal(err)
		}
		success := newCandidate(policy)
		success.State = scannerrelease.CandidateAwaitingDefinition
		success.SelectionJSON = `{"mode":"complete"}`
		success.ProposalMaxAttempts = 2
		if err := repository.CreateCandidate(
			ctx, success, command("create:"+success.ID),
		); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		claimed, err := repository.ClaimNextCandidateProposal(
			ctx, "proposal-worker-a", now.Add(time.Minute),
		)
		if err != nil || claimed == nil || claimed.ID != success.ID ||
			claimed.ProposalLeaseToken == "" || claimed.ProposalAttempt != 1 ||
			claimed.Version != 2 {
			t.Fatalf("proposal claim = %#v err=%v", claimed, err)
		}
		if status, err := repository.HeartbeatCandidateProposal(
			ctx, success.ID, "proposal-worker-a", "wrong-token", now.Add(2*time.Minute),
		); err != nil || status.Current {
			t.Fatalf("stale proposal heartbeat = %#v err=%v", status, err)
		}
		status, err := repository.HeartbeatCandidateProposal(
			ctx, success.ID, "proposal-worker-a", claimed.ProposalLeaseToken,
			now.Add(2*time.Minute),
		)
		if err != nil || !status.Current || status.Version != claimed.Version ||
			status.State != scannerrelease.CandidateAwaitingDefinition {
			t.Fatalf("current proposal heartbeat = %#v err=%v", status, err)
		}
		if other, err := repository.ClaimNextCandidateProposal(
			ctx, "proposal-worker-b", now.Add(time.Minute),
		); err != nil || other != nil {
			t.Fatalf("duplicate proposal claim = %#v err=%v", other, err)
		}
		claimed.ProposedCommit = "0123456789abcdef"
		claimed.ProposalURL = "https://git.example.test/pull/1"
		claimed.LockDigest = "sha256:proposal"
		claimed.LockURI = "oci://registry.example.test/locks@sha256:proposal"
		claimed.RiskSummaryJSON = `{"highest":"low"}`
		if _, err := repository.FinalizeCandidateProposal(
			ctx, claimed, claimed.Version, "wrong-token",
			command("stale-finalize:"+success.ID),
		); !errors.Is(err, scannerrelease.ErrLeaseNotOwned) {
			t.Fatalf("stale proposal finalize error=%v want ErrLeaseNotOwned", err)
		}
		finalized, err := repository.FinalizeCandidateProposal(
			ctx, claimed, claimed.Version, claimed.ProposalLeaseToken,
			command("finalize:"+success.ID),
		)
		if err != nil || finalized.State != scannerrelease.CandidateQueued ||
			finalized.ProposedCommit != claimed.ProposedCommit ||
			finalized.ProposalWorkerID != "" || finalized.ProposalLeaseToken != "" ||
			finalized.ProposalCompletedAt == nil || finalized.Version != 3 {
			t.Fatalf("finalized proposal = %#v err=%v", finalized, err)
		}

		retry := newCandidate(policy)
		retry.State = scannerrelease.CandidateAwaitingDefinition
		retry.SelectionJSON = `{"mode":"complete"}`
		retry.ProposalMaxAttempts = 2
		if err := repository.CreateCandidate(
			ctx, retry, command("create:"+retry.ID),
		); err != nil {
			t.Fatal(err)
		}
		first, err := repository.ClaimNextCandidateProposal(
			ctx, "proposal-worker-c", now.Add(time.Minute),
		)
		if err != nil || first == nil || first.ID != retry.ID {
			t.Fatalf("retry proposal claim = %#v err=%v", first, err)
		}
		released, err := repository.ReleaseCandidateProposal(
			ctx, retry.ID, "proposal-worker-c", first.ProposalLeaseToken,
			"proposal_execution", "credential-free failure",
			command("release:"+retry.ID),
		)
		if err != nil || released.State != scannerrelease.CandidateAwaitingDefinition ||
			released.ProposalAttempt != 1 || released.ProposalLeaseToken != "" ||
			released.ProposalErrorClass != "proposal_execution" {
			t.Fatalf("released proposal = %#v err=%v", released, err)
		}
		second, err := repository.ClaimNextCandidateProposal(
			ctx, "proposal-worker-d", time.Now().UTC().Add(time.Minute),
		)
		if err != nil || second == nil || second.ID != retry.ID ||
			second.ProposalAttempt != 2 {
			t.Fatalf("second proposal claim = %#v err=%v", second, err)
		}
		if count, err := repository.ReclaimStaleCandidateProposals(
			ctx, time.Now().UTC().Add(2*time.Minute),
		); err != nil || count != 1 {
			t.Fatalf("stale proposal reclaim count=%d err=%v", count, err)
		}
		second.ProposedCommit = "0123456789abcdef"
		if _, err := repository.FinalizeCandidateProposal(
			ctx, second, second.Version, second.ProposalLeaseToken,
			command("late-finalize:"+retry.ID),
		); !errors.Is(err, scannerrelease.ErrVersionConflict) {
			t.Fatalf("late stale proposal finalize error=%v want ErrVersionConflict", err)
		}
		exhausted, err := repository.GetCandidate(ctx, retry.ID)
		if err != nil || exhausted.State != scannerrelease.CandidateFailed ||
			exhausted.ErrorClass != "proposal_worker_lost" ||
			exhausted.ProposalCompletedAt == nil {
			t.Fatalf("exhausted proposal = %#v err=%v", exhausted, err)
		}

		stateRace := newCandidate(policy)
		stateRace.State = scannerrelease.CandidateAwaitingDefinition
		stateRace.SelectionJSON = `{"mode":"complete"}`
		if err := repository.CreateCandidate(
			ctx, stateRace, command("create:"+stateRace.ID),
		); err != nil {
			t.Fatal(err)
		}
		racingClaim, err := repository.ClaimNextCandidateProposal(
			ctx, "proposal-worker-e", time.Now().UTC().Add(time.Minute),
		)
		if err != nil || racingClaim == nil || racingClaim.ID != stateRace.ID {
			t.Fatalf("state-race claim = %#v err=%v", racingClaim, err)
		}
		if _, err := repository.TransitionCandidate(
			ctx, stateRace.ID, racingClaim.Version, scannerrelease.CandidateRejected,
			command("reject:"+stateRace.ID),
		); err != nil {
			t.Fatal(err)
		}
		raceStatus, err := repository.HeartbeatCandidateProposal(
			ctx, stateRace.ID, "proposal-worker-e", racingClaim.ProposalLeaseToken,
			time.Now().UTC().Add(time.Minute),
		)
		if err != nil || raceStatus.Current ||
			raceStatus.State != scannerrelease.CandidateRejected {
			t.Fatalf("state-race heartbeat = %#v err=%v", raceStatus, err)
		}
		if _, err := repository.FinalizeCandidateProposal(
			ctx, racingClaim, racingClaim.Version, racingClaim.ProposalLeaseToken,
			command("raced-finalize:"+stateRace.ID),
		); !errors.Is(err, scannerrelease.ErrVersionConflict) {
			t.Fatalf("state-race finalize error=%v want ErrVersionConflict", err)
		}

		concurrent := newCandidate(policy)
		concurrent.State = scannerrelease.CandidateAwaitingDefinition
		concurrent.SelectionJSON = `{"mode":"complete"}`
		if err := repository.CreateCandidate(
			ctx, concurrent, command("create:"+concurrent.ID),
		); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		claims := make(chan *scannerrelease.Candidate, 2)
		errs := make(chan error, 2)
		var wait sync.WaitGroup
		for _, workerID := range []string{"proposal-racer-a", "proposal-racer-b"} {
			workerID := workerID
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				claim, err := repository.ClaimNextCandidateProposal(
					ctx, workerID, time.Now().UTC().Add(time.Minute),
				)
				claims <- claim
				errs <- err
			}()
		}
		close(start)
		wait.Wait()
		close(claims)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("concurrent proposal claim: %v", err)
			}
		}
		claimCount := 0
		for claim := range claims {
			if claim != nil {
				claimCount++
				if claim.ID != concurrent.ID {
					t.Fatalf("concurrent proposal claimed %s want %s", claim.ID, concurrent.ID)
				}
			}
		}
		if claimCount != 1 {
			t.Fatalf("concurrent proposal claim count=%d want=1", claimCount)
		}
	})

	t.Run("schedule lease ownership expiry and terminal completion", func(t *testing.T) {
		backend := factory(t)
		t.Cleanup(func() { _ = backend.close() })
		ctx := context.Background()
		repository := backend.persistence
		now := time.Date(2026, 7, 30, 2, 0, 0, 0, time.UTC)
		key := "daily-discovery-" + uuid.NewString()
		period := "2026-07-30"
		first, acquired, err := repository.AcquireScheduleLease(
			ctx, key, period, "scheduler-a", now, now.Add(time.Minute),
		)
		if err != nil || !acquired || first.Token == "" {
			t.Fatalf("first acquisition = %#v acquired=%v err=%v", first, acquired, err)
		}
		held, acquired, err := repository.AcquireScheduleLease(
			ctx, key, period, "scheduler-b", now.Add(30*time.Second), now.Add(2*time.Minute),
		)
		if err != nil || acquired || held.Owner != "scheduler-a" {
			t.Fatalf("held acquisition = %#v acquired=%v err=%v", held, acquired, err)
		}
		taken, acquired, err := repository.AcquireScheduleLease(
			ctx, key, period, "scheduler-b", now.Add(2*time.Minute), now.Add(3*time.Minute),
		)
		if err != nil || !acquired || taken.Owner != "scheduler-b" || taken.Token == first.Token {
			t.Fatalf("expired takeover = %#v acquired=%v err=%v", taken, acquired, err)
		}
		if ok, err := repository.HeartbeatScheduleLease(
			ctx, key, period, "scheduler-a", first.Token, now.Add(2*time.Minute), now.Add(4*time.Minute),
		); err != nil || ok {
			t.Fatalf("stale heartbeat ok=%v err=%v", ok, err)
		}
		if ok, err := repository.CompleteScheduleLease(
			ctx, key, period, "scheduler-b", taken.Token, scannerrelease.LeaseCompleted,
			"discovery-run-id", now.Add(150*time.Second),
		); err != nil || !ok {
			t.Fatalf("complete lease ok=%v err=%v", ok, err)
		}
		terminal, acquired, err := repository.AcquireScheduleLease(
			ctx, key, period, "scheduler-c", now.Add(4*time.Minute), now.Add(5*time.Minute),
		)
		if err != nil || acquired || terminal.State != scannerrelease.LeaseCompleted {
			t.Fatalf("terminal acquisition = %#v acquired=%v err=%v", terminal, acquired, err)
		}
	})
}

func newPolicy(scope string, revision int64) *scannerrelease.Policy {
	return &scannerrelease.Policy{
		ID:           uuid.NewString(),
		Scope:        scope,
		Revision:     revision,
		Enabled:      true,
		ScheduleJSON: `{"timezone":"UTC"}`,
		RulesJSON:    `{"approval":"required"}`,
		CreatedBy:    "admin@example.test",
	}
}

func newRegistry() *scannerrelease.RegistryTarget {
	id := uuid.NewString()
	return &scannerrelease.RegistryTarget{
		ID:                 id,
		Name:               "registry-" + id,
		Type:               scannerrelease.RegistryManaged,
		Host:               id + ".registry.example.test",
		Namespace:          "wolf",
		TrustPolicyRef:     "trust-policy",
		PlatformPolicyJSON: `{"required":["linux/amd64"]}`,
		Enabled:            true,
		CreatedBy:          "admin@example.test",
	}
}

func newDiscovery(policy *scannerrelease.Policy) *scannerrelease.DiscoveryRun {
	id := uuid.NewString()
	return &scannerrelease.DiscoveryRun{
		ID:               id,
		Trigger:          scannerrelease.DiscoveryOnDemand,
		DefinitionCommit: "0123456789abcdef",
		PolicyID:         policy.ID,
		PolicyRevision:   policy.Revision,
		State:            scannerrelease.DiscoveryQueued,
		Actor:            "operator@example.test",
		IdempotencyKey:   "discovery:" + id,
	}
}

func newCandidate(policy *scannerrelease.Policy) *scannerrelease.Candidate {
	id := uuid.NewString()
	return &scannerrelease.Candidate{
		ID:                id,
		DefinitionCommit:  "0123456789abcdef",
		RiskSummaryJSON:   `{"highest":"low"}`,
		State:             scannerrelease.CandidateDraft,
		RequiredGatesJSON: `["smoke","parser","vulnerability","license","signature"]`,
		PolicyDecision:    "approval_required",
		PolicyID:          policy.ID,
		PolicyRevision:    policy.Revision,
		Actor:             "operator@example.test",
		IdempotencyKey:    "candidate:" + id,
	}
}

func command(key string) scannerrelease.TransitionCommand {
	return scannerrelease.TransitionCommand{
		Actor:          "operator@example.test",
		Reason:         "repository contract test",
		PolicyRevision: 1,
		IdempotencyKey: key,
		PayloadJSON:    `{"redacted":true}`,
	}
}

func TestScannerRunReleaseProvenanceRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID, repoID := seedQueueOwner(t, store)
	scan := seedQueuedScan(t, store, userID, repoID)
	record := &models.ScannerRunRecord{
		ID:                    uuid.NewString(),
		ScanID:                scan.ID,
		ToolName:              "semgrep",
		Status:                "completed",
		Image:                 "ghcr.io/example/scanner@sha256:image",
		ImageDigest:           "sha256:image",
		ScannerReleaseID:      "scanner-set-2026.31.1",
		ReleaseManifestDigest: "sha256:manifest",
		CommandJSON:           `["semgrep"]`,
	}
	if err := store.UpsertScannerRunRecord(ctx, record); err != nil {
		t.Fatalf("UpsertScannerRunRecord: %v", err)
	}
	records, err := store.ListScannerRunRecords(ctx, scan.ID)
	if err != nil {
		t.Fatalf("ListScannerRunRecords: %v", err)
	}
	if len(records) != 1 || records[0].ScannerReleaseID != record.ScannerReleaseID ||
		records[0].ReleaseManifestDigest != record.ReleaseManifestDigest {
		t.Fatalf("release provenance did not round-trip: %#v", records)
	}
}

func TestScannerReleaseMigrationsAreIdempotentAndPreserveExistingRows(t *testing.T) {
	path := t.TempDir() + "/migration.db"
	store, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	ctx := context.Background()
	user := &models.User{
		ID:           uuid.NewString(),
		Email:        uuid.NewString() + "@example.test",
		PasswordHash: "hash",
	}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	repo := &models.Repo{
		ID:            uuid.NewString(),
		UserID:        user.ID,
		Name:          "pre-feature-repo",
		SourceType:    models.SourceTypeLocal,
		SourcePath:    t.TempDir(),
		DefaultBranch: "main",
	}
	if err := store.CreateRepo(ctx, repo); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	scan := &models.Scan{
		ID:     uuid.NewString(),
		UserID: user.ID,
		RepoID: repo.ID,
		Branch: "main",
		Status: models.ScanStatusCompleted,
	}
	if err := store.CreateScan(ctx, scan); err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	if err := store.UpsertScannerRunRecord(ctx, &models.ScannerRunRecord{
		ID:       uuid.NewString(),
		ScanID:   scan.ID,
		ToolName: "semgrep",
		Status:   "completed",
	}); err != nil {
		t.Fatalf("UpsertScannerRunRecord: %v", err)
	}
	// Strip only migrations 030/031 to reproduce a populated database at the
	// 029 boundary. Existing application rows remain in place.
	if _, err := store.db.Exec(`
		PRAGMA foreign_keys = OFF;
		DROP TABLE scanner_release_events;
		DROP TABLE scanner_schedule_leases;
		DROP TABLE scanner_worker_release_status;
		DROP TABLE scanner_rollout_cohorts;
		DROP TABLE scanner_rollouts;
		DROP TABLE scanner_release_approvals;
		DROP TABLE scanner_release_artifacts;
		DROP TABLE scanner_release_images;
		DROP TABLE scanner_release_tools;
		DROP TABLE scanner_releases;
		DROP TABLE scanner_build_steps;
		DROP TABLE scanner_build_runs;
		DROP TABLE scanner_release_candidates;
		DROP TABLE scanner_update_items;
		DROP TABLE scanner_discovery_runs;
		DROP TABLE scanner_registry_targets;
		DROP TABLE scanner_update_policies;
		DROP INDEX idx_scanner_run_records_release;
		DROP INDEX idx_scanner_run_records_manifest_digest;
		ALTER TABLE scanner_run_records DROP COLUMN scanner_release_id;
		ALTER TABLE scanner_run_records DROP COLUMN release_manifest_digest;
		PRAGMA foreign_keys = ON;
	`); err != nil {
		t.Fatalf("prepare populated 029 database: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("reopen migrated store: %v", err)
	}
	defer reopened.Close()
	got, err := reopened.GetUserByID(ctx, user.ID)
	if err != nil || got.Email != user.Email {
		t.Fatalf("pre-feature row was not preserved: user=%#v err=%v", got, err)
	}
	records, err := reopened.ListScannerRunRecords(ctx, scan.ID)
	if err != nil || len(records) != 1 || records[0].ToolName != "semgrep" ||
		records[0].ScannerReleaseID != "" || records[0].ReleaseManifestDigest != "" {
		t.Fatalf("pre-feature scanner run was not preserved: records=%#v err=%v", records, err)
	}
	if err := reopened.Migrate(); err != nil {
		t.Fatalf("idempotent re-run after upgrade: %v", err)
	}
}
