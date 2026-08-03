package scannerobservability

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRegistryBoundsMetricLabels(t *testing.T) {
	registry := NewRegistry()
	registry.Enable(ComponentBuild, true)
	registry.SetState(ComponentBuild, "busy")
	registry.ObserveClaim(ComponentBuild, "worker-id-that-must-not-be-a-label")
	registry.ObserveLease(ComponentBuild, "arbitrary-release-id")
	registry.ObserveRetry(ComponentBuild, "candidate-123")
	registry.ObserveResult(ComponentBuild, "scanner-set-2026.31.1")
	registry.ObserveRun(ComponentBuild, "arbitrary-error", time.Second)

	var output bytes.Buffer
	if err := registry.RenderPrometheus(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, forbidden := range []string{
		"worker-id-that-must-not-be-a-label",
		"arbitrary-release-id",
		"candidate-123",
		"scanner-set-2026.31.1",
		"arbitrary-error",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("unbounded label %q reached metrics:\n%s", forbidden, text)
		}
	}
	for _, expected := range []string{
		`wolf_scanner_release_claims_total{component="build",result="error"} 1`,
		`wolf_scanner_release_lease_events_total{component="build",result="error"} 1`,
		`wolf_scanner_release_retries_total{component="build",reason="contention"} 1`,
		`wolf_scanner_release_results_total{component="build",state="failed"} 1`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("metrics omitted %q:\n%s", expected, text)
		}
	}
}

func TestHealthDistinguishesDisabledActiveDatabaseAndStuck(t *testing.T) {
	registry := NewRegistry()
	if snapshot := registry.Snapshot(context.Background()); snapshot.Status != "disabled" || !snapshot.Ready {
		t.Fatalf("disabled snapshot = %#v", snapshot)
	}

	registry.SetDatabaseCheck(func(context.Context) error { return nil })
	registry.Enable(ComponentScheduler, true)
	registry.SetState(ComponentScheduler, "active")
	if snapshot := registry.Snapshot(context.Background()); snapshot.Status != "active" || !snapshot.Ready {
		t.Fatalf("active snapshot = %#v", snapshot)
	}

	registry.SetStuckWork(ComponentScheduler, "expired_lease", 2)
	if snapshot := registry.Snapshot(context.Background()); snapshot.Status != "degraded" || snapshot.Ready ||
		componentFrom(snapshot, ComponentScheduler).Status != "stale_or_stuck" {
		t.Fatalf("stuck snapshot = %#v", snapshot)
	}
	registry.SetStuckWork(ComponentScheduler, "expired_lease", 0)
	registry.SetDatabaseCheck(func(context.Context) error { return errors.New("down") })
	if snapshot := registry.Snapshot(context.Background()); snapshot.Status != "database_unavailable" || snapshot.Ready {
		t.Fatalf("database snapshot = %#v", snapshot)
	}
}

func TestRestoreMaintenanceDisablesReadinessAndPublishesBoundedGauge(t *testing.T) {
	registry := NewRegistry()
	registry.SetDatabaseCheck(func(context.Context) error { return nil })
	registry.SetMaintenanceCheck(func(context.Context) (bool, error) {
		return true, nil
	})
	registry.Enable(ComponentRollout, true)
	registry.SetState(ComponentRollout, "active")
	snapshot := registry.Snapshot(context.Background())
	if snapshot.Ready || snapshot.Status != "restore_maintenance" ||
		snapshot.Maintenance != "restore" {
		t.Fatalf("maintenance snapshot = %#v", snapshot)
	}
	var output bytes.Buffer
	if err := registry.RenderPrometheus(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(
		output.String(),
		"wolf_scanner_release_restore_maintenance_active 1",
	) {
		t.Fatalf("maintenance metric missing:\n%s", output.String())
	}
}

func TestHealthSnapshotIncludesBoundedDashboardSummaries(t *testing.T) {
	registry := NewRegistry()
	registry.Enable(ComponentBuild, true)
	registry.SetState(ComponentBuild, "active")
	registry.ObserveRun(ComponentBuild, "success", 2*time.Second)
	registry.ObserveRun(ComponentBuild, "unexpected-sensitive-value", 4*time.Second)
	registry.ObserveResult(ComponentBuild, "completed")
	registry.ObserveResult(ComponentBuild, "candidate-unbounded-value")
	registry.SetQueueDepth(ComponentBuild, "queued", 3)

	build := componentFrom(registry.Snapshot(context.Background()), ComponentBuild)
	if build.RunCounts["success"] != 1 || build.RunCounts["error"] != 1 ||
		build.ResultCounts["completed"] != 1 ||
		build.ResultCounts["failed"] != 1 ||
		build.QueueDepth["pending"] != 3 ||
		build.AverageRunDurationMS != 3_000 {
		t.Fatalf("bounded dashboard summary = %#v", build)
	}
	if _, present := build.RunCounts["unexpected-sensitive-value"]; present {
		t.Fatal("unbounded run result reached health response")
	}
	if _, present := build.ResultCounts["candidate-unbounded-value"]; present {
		t.Fatal("unbounded work state reached health response")
	}
}

func componentFrom(snapshot HealthSnapshot, component Component) ComponentStatus {
	for _, status := range snapshot.Components {
		if status.Component == component {
			return status
		}
	}
	return ComponentStatus{}
}

func TestHTTPHealthAndReadinessContracts(t *testing.T) {
	registry := NewRegistry()
	registry.Enable(ComponentDiscovery, true)
	registry.SetState(ComponentDiscovery, "active")
	registry.SetDatabaseCheck(func(context.Context) error { return errors.New("down") })
	handler := registry.Handler()

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), "database_unavailable") {
		t.Fatalf("health = %d %s", health.Code, health.Body.String())
	}
	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready = %d %s", ready.Code, ready.Body.String())
	}
}
