package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestScannerAlertRepositoryContractPostgres(t *testing.T) {
	runScannerAlertRepositoryContract(t, newPostgresReleaseContractBackend)
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
