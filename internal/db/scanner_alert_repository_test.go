package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestScannerAlertRepositoryContractSQLite(t *testing.T) {
	runScannerAlertRepositoryContract(t, newSQLiteReleaseContractBackend)
}

func TestScannerAlertRepositoryContractPostgres(t *testing.T) {
	runScannerAlertRepositoryContract(t, newPostgresReleaseContractBackend)
}

func TestScannerAlertEvaluatorCoversAllConditionClassesSQLite(t *testing.T) {
	backend := newSQLiteReleaseContractBackend(t)
	t.Cleanup(func() { _ = backend.close() })
	ctx := context.Background()
	repository := backend.persistence
	policy := newPolicy("alert-all:"+uuid.NewString(), 1)
	policy.RulesJSON = `{}`
	if err := repository.CreatePolicy(ctx, policy); err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	completed := newDiscovery(policy)
	if err := repository.CreateDiscoveryRun(
		ctx, completed, command("all-discovery-create:"+completed.ID),
	); err != nil {
		t.Fatal(err)
	}
	for _, state := range []scannerrelease.DiscoveryState{
		scannerrelease.DiscoveryResolving,
		scannerrelease.DiscoveryComparing,
		scannerrelease.DiscoveryProposing,
		scannerrelease.DiscoveryCompleted,
	} {
		updated, err := repository.TransitionDiscovery(
			ctx, completed.ID, completed.Version, state,
			command("all-discovery:"+string(state)+":"+completed.ID),
		)
		if err != nil {
			t.Fatalf("transition discovery to %s: %v", state, err)
		}
		completed = updated
	}
	for index := 0; index < 2; index++ {
		queued := newDiscovery(policy)
		if err := repository.CreateDiscoveryRun(
			ctx, queued, command("all-queued:"+queued.ID),
		); err != nil {
			t.Fatal(err)
		}
	}

	registry := newRegistry()
	registry.Type = scannerrelease.RegistryMirror
	if err := repository.CreateRegistryTarget(ctx, registry); err != nil {
		t.Fatal(err)
	}
	evaluationTime := time.Date(2090, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := repository.UpdateRegistryObservation(
		ctx, registry.ID, scannerrelease.RegistryObservation{
			CheckedAt: evaluationTime.Add(-time.Hour), HealthStatus: "degraded",
			DigestParityStatus: "mismatched", DetailJSON: `{}`,
		},
	); err != nil {
		t.Fatal(err)
	}

	candidate := newCandidate(policy)
	if err := repository.CreateCandidate(
		ctx, candidate, command("all-candidate:"+candidate.ID),
	); err != nil {
		t.Fatal(err)
	}
	candidate, err := repository.TransitionCandidate(
		ctx, candidate.ID, candidate.Version, scannerrelease.CandidateQueued,
		command("all-candidate-queued:"+candidate.ID),
	)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err = repository.TransitionCandidate(
		ctx, candidate.ID, candidate.Version, scannerrelease.CandidateBlocked,
		command("all-candidate-blocked:"+candidate.ID),
	)
	if err != nil {
		t.Fatal(err)
	}

	releaseID := uuid.NewString()
	inventory := &scannerrelease.ReleaseInventory{
		Release: scannerrelease.Release{
			ID: releaseID, Name: "scanner-set-alert-" + releaseID,
			CandidateID: candidate.ID, LockDigest: "sha256:lock",
			ManifestDigest: "sha256:manifest-" + releaseID,
			ManifestURI:    "oci://registry/release@" + releaseID,
			State:          scannerrelease.ReleaseStable, SignerIdentity: "test",
			PolicyID: policy.ID, PolicyRevision: policy.Revision,
			DefinitionCommit: "0123456789abcdef",
			PublishedAt:      evaluationTime.Add(-180 * 24 * time.Hour),
		},
		Images: []scannerrelease.ReleaseImage{{
			ImageKey: "default", RegistryTargetID: registry.ID,
			Repository: "wolf-scanners", Digest: "sha256:image",
			PlatformDigests: `{}`, SignatureStatus: "unverified",
		}},
	}
	if err := repository.CreateRelease(
		ctx, inventory, command("all-release:"+releaseID),
	); err != nil {
		t.Fatal(err)
	}
	rollout := &scannerrelease.Rollout{
		ID: uuid.NewString(), Target: "production",
		ToReleaseID: releaseID, Strategy: "canary_then_stable",
		PolicySnapshotJSON: `{}`, Actor: "test",
	}
	if err := repository.CreateRollout(
		ctx, rollout, nil, command("all-rollout:"+rollout.ID),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.TransitionRollout(
		ctx, rollout.ID, rollout.Version, scannerrelease.RolloutFailed,
		command("all-rollout-failed:"+rollout.ID),
	); err != nil {
		t.Fatal(err)
	}
	if _, acquired, err := repository.AcquireScheduleLease(
		ctx, "expired-fixture", "period", "lost-worker",
		evaluationTime.Add(-time.Hour), evaluationTime.Add(-time.Minute),
	); err != nil || !acquired {
		t.Fatalf("expired schedule lease acquired=%v err=%v", acquired, err)
	}

	summary, err := repository.EvaluateAlerts(
		ctx, scannerrelease.AlertEvaluationRequest{
			PolicyID: policy.ID, PolicyScope: policy.Scope,
			PolicyRevision: policy.Revision,
			MissedDiscovery: scannerrelease.AlertDurationThreshold{
				Enabled: true, After: time.Hour,
			},
			StaleStableRelease: scannerrelease.AlertDurationThreshold{
				Enabled: true, After: 90 * 24 * time.Hour,
			},
			QueueBacklog: scannerrelease.AlertQueueThreshold{
				Enabled: true, MaxDepth: 1,
			},
			LeaseChurn: scannerrelease.AlertCountThreshold{
				Enabled: true, Count: 100, Window: time.Hour,
			},
			RepeatedGateFailure: scannerrelease.AlertCountThreshold{
				Enabled: true, Count: 1, Window: 100 * 365 * 24 * time.Hour,
			},
			MirrorDrift: true, RolloutFailure: true, SignatureHealth: true,
		},
		evaluationTime,
	)
	if err != nil {
		t.Fatalf("EvaluateAlerts: %v", err)
	}
	if summary.Opened != 8 ||
		summary.Active.OpenWarning != 4 ||
		summary.Active.OpenCritical != 4 {
		t.Fatalf("all-condition summary = %#v", summary)
	}
	page, err := repository.ListAlerts(
		ctx, scannerrelease.AlertFilter{},
		scannerrelease.PageRequest{Limit: 20},
	)
	if err != nil {
		t.Fatal(err)
	}
	kinds := make(map[scannerrelease.AlertKind]bool)
	for _, alert := range page.Items {
		kinds[alert.Kind] = true
	}
	for _, kind := range []scannerrelease.AlertKind{
		scannerrelease.AlertMissedDiscovery,
		scannerrelease.AlertStaleStableRelease,
		scannerrelease.AlertQueueBacklog,
		scannerrelease.AlertLeaseChurn,
		scannerrelease.AlertRepeatedGateFailure,
		scannerrelease.AlertMirrorDrift,
		scannerrelease.AlertRolloutFailure,
		scannerrelease.AlertSignatureHealth,
	} {
		if !kinds[kind] {
			t.Fatalf("missing alert kind %q in %#v", kind, page.Items)
		}
	}
}

func runScannerAlertRepositoryContract(
	t *testing.T,
	factory func(*testing.T) releaseContractBackend,
) {
	t.Helper()
	backend := factory(t)
	t.Cleanup(func() { _ = backend.close() })
	ctx := context.Background()
	repository := backend.persistence
	// The optional PostgreSQL contract database is shared by contract tests.
	// Alert fingerprints are intentionally global, so clear only prior alert
	// aggregates before exercising a fresh lifecycle.
	_, _ = backend.exec(ctx,
		`DELETE FROM scanner_release_notifications WHERE aggregate_type = 'alert'`)
	_, _ = backend.exec(ctx,
		`DELETE FROM scanner_release_events WHERE aggregate_type = 'alert'`)
	_, _ = backend.exec(ctx, `DELETE FROM scanner_release_alerts`)

	policy := newPolicy("alert:"+uuid.NewString(), 1)
	policy.RulesJSON = `{"notifications":{"destinations":["webhook:security-operations"]}}`
	if err := repository.CreatePolicy(ctx, policy); err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	run := newDiscovery(policy)
	if err := repository.CreateDiscoveryRun(
		ctx, run, command("alert-discovery-create:"+run.ID),
	); err != nil {
		t.Fatalf("CreateDiscoveryRun: %v", err)
	}
	for index, state := range []scannerrelease.DiscoveryState{
		scannerrelease.DiscoveryResolving,
		scannerrelease.DiscoveryComparing,
		scannerrelease.DiscoveryProposing,
		scannerrelease.DiscoveryCompleted,
	} {
		updated, err := repository.TransitionDiscovery(
			ctx, run.ID, run.Version, state,
			command("alert-discovery-transition:"+uuid.NewString()),
		)
		if err != nil {
			t.Fatalf("TransitionDiscovery[%d]: %v", index, err)
		}
		run = updated
	}

	evaluationTime := time.Date(2090, 1, 1, 0, 0, 0, 0, time.UTC)
	request := scannerrelease.AlertEvaluationRequest{
		PolicyID: policy.ID, PolicyScope: policy.Scope,
		PolicyRevision: policy.Revision,
		MissedDiscovery: scannerrelease.AlertDurationThreshold{
			Enabled: true, After: time.Hour,
		},
	}
	opened, err := repository.EvaluateAlerts(ctx, request, evaluationTime)
	if err != nil || opened.Opened != 1 || opened.Active.OpenWarning != 1 {
		t.Fatalf("open alert summary = %#v err=%v", opened, err)
	}
	steady, err := repository.EvaluateAlerts(
		ctx, request, evaluationTime.Add(time.Minute),
	)
	if err != nil || steady.Opened != 0 || steady.Reopened != 0 ||
		steady.Resolved != 0 || steady.Active.OpenWarning != 1 {
		t.Fatalf("steady alert summary = %#v err=%v", steady, err)
	}

	page, err := repository.ListAlerts(
		ctx,
		scannerrelease.AlertFilter{State: scannerrelease.AlertOpen},
		scannerrelease.PageRequest{Limit: 10},
	)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("ListAlerts(open) = %#v err=%v", page, err)
	}
	alert := &page.Items[0]
	if alert.Kind != scannerrelease.AlertMissedDiscovery ||
		alert.Generation != 1 || alert.TriggerCount != 2 ||
		alert.Fingerprint == "" || len(alert.EvidenceJSON) > maxAlertEvidenceBytes {
		t.Fatalf("opened alert = %#v", alert)
	}

	request.MissedDiscovery.After = 200 * 365 * 24 * time.Hour
	resolved, err := repository.EvaluateAlerts(
		ctx, request, evaluationTime.Add(2*time.Minute),
	)
	if err != nil || resolved.Resolved != 1 || resolved.Active.Resolved != 1 {
		t.Fatalf("resolve alert summary = %#v err=%v", resolved, err)
	}
	request.MissedDiscovery.After = time.Hour
	reopened, err := repository.EvaluateAlerts(
		ctx, request, evaluationTime.Add(3*time.Minute),
	)
	if err != nil || reopened.Reopened != 1 || reopened.Active.OpenWarning != 1 {
		t.Fatalf("reopen alert summary = %#v err=%v", reopened, err)
	}
	alert, err = repository.GetAlert(ctx, alert.ID)
	if err != nil || alert.State != scannerrelease.AlertOpen ||
		alert.Generation != 2 || alert.TriggerCount != 3 {
		t.Fatalf("reopened alert = %#v err=%v", alert, err)
	}

	notifications, err := repository.ListNotifications(
		ctx, scannerrelease.NotificationFilter{},
		scannerrelease.PageRequest{Limit: 100},
	)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	lifecycleRows := 0
	for _, notification := range notifications.Items {
		if notification.AggregateID == alert.ID {
			lifecycleRows++
			if notification.PolicyID != policy.ID {
				t.Fatalf("notification lost policy identity: %#v", notification)
			}
		}
	}
	// Three lifecycle events, each with one delivered administrator record and
	// one pending external outbox record.
	if lifecycleRows != 6 {
		t.Fatalf("alert lifecycle notification rows = %d, want 6", lifecycleRows)
	}

	// A missing baseline is unknown rather than healthy. Disabling the only
	// completed run must therefore preserve the current alert instead of
	// falsely resolving it.
	if _, err := backend.exec(
		ctx, `UPDATE scanner_discovery_runs SET state = ? WHERE id = ?`,
		scannerrelease.DiscoveryFailed, run.ID,
	); err != nil {
		t.Fatalf("remove discovery baseline: %v", err)
	}
	unknown, err := repository.EvaluateAlerts(
		ctx, request, evaluationTime.Add(4*time.Minute),
	)
	if err != nil || unknown.Resolved != 0 || unknown.Active.OpenWarning != 1 {
		t.Fatalf("unknown-baseline evaluation = %#v err=%v", unknown, err)
	}
}
