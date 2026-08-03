package scanneralertworker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/scanneralertworker"
	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestAcceleratedClockLifecycleAndReplicaLease(t *testing.T) {
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	persistence := store.ScannerReleases()
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	createPolicy(t, persistence, 1, 1)
	for index := 0; index < 2; index++ {
		run := &scannerrelease.DiscoveryRun{
			ID: uuid.NewString(), Trigger: scannerrelease.DiscoveryOnDemand,
			DefinitionCommit: "0123456789abcdef",
			PolicyID:         "alert-policy-1", PolicyRevision: 1,
			State: scannerrelease.DiscoveryQueued, Actor: "test",
			IdempotencyKey: "alert-queue:" + uuid.NewString(),
		}
		if err := persistence.CreateDiscoveryRun(
			ctx, run, scannerrelease.TransitionCommand{
				Actor: "test", Reason: "accelerated clock fixture",
				PolicyRevision: 1, IdempotencyKey: "create:" + run.ID,
			},
		); err != nil {
			t.Fatalf("CreateDiscoveryRun: %v", err)
		}
	}
	observer := scannerobservability.NewRegistry()
	observer.Enable(scannerobservability.ComponentAlert, true)
	workerA := newWorker(t, persistence, "replica-a", &now, observer)
	workerB := newWorker(t, persistence, "replica-b", &now, observer)

	processed, err := workerA.RunOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("worker A first pass processed=%v err=%v", processed, err)
	}
	processed, err = workerB.RunOnce(ctx)
	if err != nil || processed {
		t.Fatalf("worker B contended pass processed=%v err=%v", processed, err)
	}
	counts, err := persistence.AlertCounts(ctx)
	if err != nil || counts.OpenWarning != 1 {
		t.Fatalf("opened counts = %#v err=%v", counts, err)
	}

	createPolicy(t, persistence, 2, 10)
	now = now.Add(time.Minute)
	if processed, err = workerA.RunOnce(ctx); err != nil || !processed {
		t.Fatalf("resolution pass processed=%v err=%v", processed, err)
	}
	counts, err = persistence.AlertCounts(ctx)
	if err != nil || counts.OpenWarning != 0 || counts.Resolved != 1 {
		t.Fatalf("resolved counts = %#v err=%v", counts, err)
	}

	createPolicy(t, persistence, 3, 1)
	now = now.Add(time.Minute)
	if processed, err = workerB.RunOnce(ctx); err != nil || !processed {
		t.Fatalf("reopen pass processed=%v err=%v", processed, err)
	}
	page, err := persistence.ListAlerts(
		ctx, scannerrelease.AlertFilter{},
		scannerrelease.PageRequest{Limit: 10},
	)
	if err != nil || len(page.Items) != 1 ||
		page.Items[0].Generation != 2 ||
		page.Items[0].State != scannerrelease.AlertOpen {
		t.Fatalf("reopened alerts = %#v err=%v", page, err)
	}

	var metrics bytes.Buffer
	if err := observer.RenderPrometheus(ctx, &metrics); err != nil {
		t.Fatalf("RenderPrometheus: %v", err)
	}
	if !strings.Contains(
		metrics.String(),
		`wolf_scanner_release_alerts{severity="warning"} 1`,
	) {
		t.Fatalf("alert metric missing:\n%s", metrics.String())
	}
}

func newWorker(
	t *testing.T,
	store scanneralertworker.Store,
	id string,
	now *time.Time,
	observer scannerobservability.Observer,
) *scanneralertworker.Worker {
	t.Helper()
	worker, err := scanneralertworker.New(scanneralertworker.Config{
		Store: store, WorkerID: id, PolicyScope: "global",
		Interval: time.Minute, HeartbeatInterval: time.Second,
		LeaseDuration: 3 * time.Second, Once: true,
		Now: func() time.Time { return *now }, Observer: observer,
	})
	if err != nil {
		t.Fatalf("New worker: %v", err)
	}
	return worker
}

func createPolicy(
	t *testing.T,
	store scannerrelease.PolicyRepository,
	revision int64,
	queueDepth int,
) {
	t.Helper()
	rules := scannerpolicy.Default()
	rules.Revision = revision
	rules.Alerts.QueueBacklog.Enabled = true
	rules.Alerts.QueueBacklog.MaxDepth = queueDepth
	rules.Alerts.QueueBacklog.MaxAgeText = ""
	encoded, err := json.Marshal(rules)
	if err != nil {
		t.Fatalf("marshal policy: %v", err)
	}
	policy := &scannerrelease.Policy{
		ID:    "alert-policy-" + string(rune('0'+revision)),
		Scope: "global", Revision: revision, Enabled: true,
		ScheduleJSON: `{}`, RulesJSON: string(encoded), CreatedBy: "test",
	}
	if err := store.CreatePolicy(context.Background(), policy); err != nil {
		t.Fatalf("CreatePolicy(%d): %v", revision, err)
	}
}
