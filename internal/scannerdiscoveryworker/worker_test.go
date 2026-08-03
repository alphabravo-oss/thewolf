package scannerdiscoveryworker_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/scannerdiscovery"
	"github.com/alphabravocompany/thewolf/internal/scannerdiscoveryworker"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

type runnerFunc func(context.Context, scannerrelease.DiscoveryRun) (scannerdiscovery.Run, error)

func (f runnerFunc) Discover(
	ctx context.Context,
	run scannerrelease.DiscoveryRun,
) (scannerdiscovery.Run, error) {
	return f(ctx, run)
}

func TestWorkerPersistsPartialRunAndRedactedItems(t *testing.T) {
	store, repository, run := newDiscoveryStore(t)
	defer store.Close()
	now := time.Now().UTC()
	runner := runnerFunc(func(
		_ context.Context,
		claimed scannerrelease.DiscoveryRun,
	) (scannerdiscovery.Run, error) {
		if claimed.ScopeJSON != `{"mode":"complete"}` {
			t.Fatalf("claimed scope = %s", claimed.ScopeJSON)
		}
		return scannerdiscovery.Run{
			SchemaVersion:    scannerdiscovery.SchemaVersion,
			DefinitionDigest: "sha256:definition",
			LockDigest:       "sha256:lock",
			Scope:            scannerdiscovery.CompleteScope(),
			State:            scannerdiscovery.RunPartial,
			Coverage:         0.5,
			Counts: scannerdiscovery.Counts{
				Total: 2, Covered: 1, UpdateAvailable: 1, Unreachable: 1,
			},
			Items: []scannerdiscovery.ItemResult{
				{
					Item: scannerdiscovery.Item{
						ID:             scannerdiscovery.ComponentID{Kind: scannerdiscovery.ComponentTool, Name: "semgrep"},
						CurrentValue:   "1.0.0",
						CurrentDigest:  "sha256:current",
						DefinitionRisk: scannerdiscovery.RiskMedium,
						Source: scannerdiscovery.Source{
							Type: "github-release",
							URL:  "https://user:password@updates.example/releases?token=secret-value",
						},
						Metadata: map[string]string{"api_token": "secret-value"},
					},
					Status:          scannerdiscovery.StatusUpdate,
					AvailableValue:  "1.0.1",
					AvailableDigest: "sha256:available",
					Risk: scannerdiscovery.RiskResult{
						Level: scannerdiscovery.RiskMedium, Reasons: []string{"minor version update"},
					},
					Evidence: scannerdiscovery.Evidence{
						SourceURL: "https://user:password@updates.example/releases?api_key=secret-value",
						Attributes: map[string]string{
							"authorization": "Bearer secret-value",
							"channel":       "stable",
						},
					},
					Resolver:  "fake-resolver",
					Attempts:  1,
					CheckedAt: now,
				},
				{
					Item: scannerdiscovery.Item{
						ID: scannerdiscovery.ComponentID{Kind: scannerdiscovery.ComponentToolchain, Name: "go"},
					},
					Status:     scannerdiscovery.StatusUnreachable,
					Risk:       scannerdiscovery.RiskResult{Level: scannerdiscovery.RiskNone},
					ErrorClass: scannerdiscovery.ErrorAuthentication,
					Error:      "authorization=Bearer secret-value",
					Resolver:   "fake-resolver",
					Attempts:   2,
					CheckedAt:  now,
				},
			},
			StartedAt: now.Add(-time.Second), CompletedAt: now,
		}, nil
	})
	worker := newWorker(t, repository, runner)
	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	got, err := repository.GetDiscoveryRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != scannerrelease.DiscoveryCompleted ||
		got.ErrorClass != "partial_coverage" || got.Coverage != 0.5 ||
		got.TotalCount != 2 || got.CoveredCount != 1 ||
		got.AvailableCount != 1 || got.UnreachableCount != 1 ||
		got.WorkerID != "" || got.LeaseToken != "" {
		t.Fatalf("persisted partial discovery = %#v", got)
	}
	items, err := repository.ListUpdateItems(context.Background(), run.ID)
	if err != nil || len(items) != 2 {
		t.Fatalf("ListUpdateItems = %#v err=%v", items, err)
	}
	var updateItem, unreachableItem scannerrelease.UpdateItem
	for _, item := range items {
		switch item.ComponentName {
		case "semgrep":
			updateItem = item
		case "go":
			unreachableItem = item
		}
	}
	encoded := updateItem.SourceEvidenceJSON + updateItem.CompatibilityJSON +
		unreachableItem.ErrorDetail
	if strings.Contains(encoded, "secret-value") || strings.Contains(encoded, "user:password") {
		t.Fatalf("persisted discovery item leaked credential: %s", encoded)
	}
	if !strings.Contains(encoded, "[REDACTED]") ||
		updateItem.Status != string(scannerdiscovery.StatusUpdate) ||
		updateItem.AvailableDigest != "sha256:available" ||
		unreachableItem.ErrorClass != string(scannerdiscovery.ErrorAuthentication) {
		t.Fatalf("persisted items = %#v", items)
	}
}

func TestWorkerPersistsRunnerFailure(t *testing.T) {
	store, repository, run := newDiscoveryStore(t)
	defer store.Close()
	worker := newWorker(t, repository, runnerFunc(func(
		context.Context,
		scannerrelease.DiscoveryRun,
	) (scannerdiscovery.Run, error) {
		return scannerdiscovery.Run{}, errors.New("authorization=Bearer secret-value")
	}))
	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	got, err := repository.GetDiscoveryRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != scannerrelease.DiscoveryFailed ||
		got.ErrorClass != "discovery_execution" ||
		strings.Contains(got.ErrorDetail, "secret-value") ||
		!strings.Contains(got.ErrorDetail, "[REDACTED]") {
		t.Fatalf("persisted failed discovery = %#v", got)
	}
}

func TestWorkerCooperativelyCancelsClaim(t *testing.T) {
	store, repository, run := newDiscoveryStore(t)
	defer store.Close()
	started := make(chan struct{})
	runner := runnerFunc(func(
		ctx context.Context,
		_ scannerrelease.DiscoveryRun,
	) (scannerdiscovery.Run, error) {
		close(started)
		<-ctx.Done()
		now := time.Now().UTC()
		return scannerdiscovery.Run{
			State: scannerdiscovery.RunCancelled,
			Scope: scannerdiscovery.CompleteScope(),
			Items: []scannerdiscovery.ItemResult{{
				Item: scannerdiscovery.Item{
					ID: scannerdiscovery.ComponentID{Kind: scannerdiscovery.ComponentTool, Name: "semgrep"},
				},
				Status: scannerdiscovery.StatusUnreachable, ErrorClass: scannerdiscovery.ErrorCancelled,
				Error: ctx.Err().Error(), CheckedAt: now,
			}},
			Counts:      scannerdiscovery.Counts{Total: 1, Unreachable: 1},
			CompletedAt: now,
		}, nil
	})
	worker := newWorker(t, repository, runner)
	done := make(chan error, 1)
	go func() {
		_, err := worker.RunOnce(context.Background())
		done <- err
	}()
	<-started
	if requested, err := repository.RequestDiscoveryCancellation(
		context.Background(), run.ID,
		scannerrelease.TransitionCommand{
			Actor: "operator", Reason: "test cancellation",
			IdempotencyKey: "cancel:" + run.ID, PayloadJSON: "{}",
		},
		time.Now().UTC(),
	); err != nil || !requested {
		t.Fatalf("RequestDiscoveryCancellation requested=%v err=%v", requested, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not cooperate with cancellation")
	}
	got, err := repository.GetDiscoveryRun(context.Background(), run.ID)
	if err != nil || got.State != scannerrelease.DiscoveryCancelled ||
		got.CancelRequestedAt == nil || got.CompletedAt == nil {
		t.Fatalf("cancelled discovery = %#v err=%v", got, err)
	}
}

func TestWorkerDoesNotFinalizeAfterLeaseReclaim(t *testing.T) {
	store, repository, run := newDiscoveryStore(t)
	defer store.Close()
	started := make(chan struct{})
	runner := runnerFunc(func(
		ctx context.Context,
		_ scannerrelease.DiscoveryRun,
	) (scannerdiscovery.Run, error) {
		close(started)
		<-ctx.Done()
		return scannerdiscovery.Run{}, ctx.Err()
	})
	worker := newWorker(t, repository, runner)
	done := make(chan error, 1)
	go func() {
		_, err := worker.RunOnce(context.Background())
		done <- err
	}()
	<-started
	if count, err := repository.ReclaimStaleDiscoveryRuns(
		context.Background(), time.Now().UTC().Add(time.Hour),
	); err != nil || count != 1 {
		t.Fatalf("ReclaimStaleDiscoveryRuns count=%d err=%v", count, err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, scannerdiscoveryworker.ErrLeaseLost) {
			t.Fatalf("RunOnce error = %v, want ErrLeaseLost", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop after lease reclaim")
	}
	got, err := repository.GetDiscoveryRun(context.Background(), run.ID)
	if err != nil || got.State != scannerrelease.DiscoveryQueued ||
		got.CompletedAt != nil {
		t.Fatalf("reclaimed discovery = %#v err=%v", got, err)
	}
}

func TestDecodeScopeCompatibility(t *testing.T) {
	tests := []struct {
		name    string
		encoded string
		mode    scannerdiscovery.ScopeMode
		tools   int
		wantErr bool
	}{
		{name: "canonical", encoded: `{"mode":"complete"}`, mode: scannerdiscovery.ScopeComplete},
		{name: "legacy object", encoded: `{"type":"all"}`, mode: scannerdiscovery.ScopeComplete},
		{name: "legacy string", encoded: `"complete"`, mode: scannerdiscovery.ScopeComplete},
		{name: "selected", encoded: `{"mode":"selected","tools":["semgrep"]}`, mode: scannerdiscovery.ScopeSelected, tools: 1},
		{name: "empty selected", encoded: `{"mode":"selected"}`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scope, err := scannerdiscoveryworker.DecodeScope(test.encoded)
			if (err != nil) != test.wantErr {
				t.Fatalf("DecodeScope error = %v, wantErr=%v", err, test.wantErr)
			}
			if err == nil && (scope.Mode != test.mode || len(scope.Tools) != test.tools) {
				t.Fatalf("scope = %#v", scope)
			}
		})
	}
}

func newDiscoveryStore(
	t *testing.T,
) (*db.SQLiteStore, scannerrelease.Persistence, *scannerrelease.DiscoveryRun) {
	t.Helper()
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	repository := store.ScannerReleases()
	policy := &scannerrelease.Policy{
		ID: uuid.NewString(), Scope: "test:" + uuid.NewString(), Revision: 1,
		Enabled: true, ScheduleJSON: "{}", RulesJSON: "{}", CreatedBy: "test",
	}
	if err := repository.CreatePolicy(context.Background(), policy); err != nil {
		store.Close()
		t.Fatal(err)
	}
	run := &scannerrelease.DiscoveryRun{
		ID: uuid.NewString(), Trigger: scannerrelease.DiscoveryOnDemand,
		DefinitionCommit: "commit-1", PolicyID: policy.ID, PolicyRevision: 1,
		ScopeJSON: `{"mode":"complete"}`, State: scannerrelease.DiscoveryQueued,
		Actor: "operator", IdempotencyKey: "discovery:" + uuid.NewString(),
	}
	if err := repository.CreateDiscoveryRun(
		context.Background(), run,
		scannerrelease.TransitionCommand{
			Actor: "operator", Reason: "test discovery",
			IdempotencyKey: run.IdempotencyKey, PayloadJSON: "{}",
		},
	); err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store, repository, run
}

func newWorker(
	t *testing.T,
	repository scannerrelease.DiscoveryRepository,
	runner scannerdiscoveryworker.Runner,
) *scannerdiscoveryworker.Worker {
	t.Helper()
	worker, err := scannerdiscoveryworker.New(scannerdiscoveryworker.Config{
		Store: repository, Runner: runner, WorkerID: "discovery-worker",
		PollInterval: 5 * time.Millisecond, HeartbeatInterval: 5 * time.Millisecond,
		LeaseDuration: 50 * time.Millisecond, DrainTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}
