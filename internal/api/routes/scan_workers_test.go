package routes

import (
	"context"
	"errors"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkerReadinessUsesBackendCapacityAndHeartbeat(t *testing.T) {
	now := time.Now().UTC()
	stale := now.Add(-2 * time.Minute)
	workers := []models.ScanWorker{
		{Backend: "kubernetes", Status: "ready", Capacity: 2, HeartbeatAt: now},
		{Backend: "docker", Status: "ready", Capacity: 2, HeartbeatAt: stale},
		{Backend: "docker", Status: "stopping", Capacity: 2, HeartbeatAt: now},
		{Backend: "docker", Status: "ready", Capacity: 2, HeartbeatAt: now.Add(time.Hour)},
	}
	got := workerReadiness(workers, "docker", now)
	if got.Status != "unavailable" || got.ActiveWorkers != 0 || got.LastActiveHeartbeat == nil || !got.LastActiveHeartbeat.Equal(stale) {
		t.Fatalf("false readiness: %+v", got)
	}
	workers = append(workers, models.ScanWorker{Backend: "docker", Status: "busy", Capacity: 1, ActiveScans: 1, HeartbeatAt: now})
	if got := workerReadiness(workers, "docker", now); got.Status != "busy" || got.ActiveWorkers != 1 || got.AvailableSlots != 0 {
		t.Fatalf("busy: %+v", got)
	}
	workers = append(workers, models.ScanWorker{Backend: "docker", Status: "ready", Capacity: 3, ActiveScans: 1, HeartbeatAt: now})
	if got := workerReadiness(workers, "docker", now); got.Status != "ready" || got.ActiveWorkers != 2 || got.AvailableSlots != 2 {
		t.Fatalf("ready: %+v", got)
	}
}

type unavailableWorkerStore struct{ db.Store }

func (unavailableWorkerStore) ListScanWorkers(context.Context, time.Time) ([]models.ScanWorker, error) {
	return nil, errors.New("private storage error")
}

func TestRuntimeCapabilitiesDoesNotInventWorkerHealth(t *testing.T) {
	previous := DefaultHandler
	DefaultHandler = &Handler{Store: unavailableWorkerStore{}}
	t.Cleanup(func() { DefaultHandler = previous })
	t.Setenv("WOLF_SCAN_EXECUTION_MODE", "queue")
	response := httptest.NewRecorder()
	RuntimeCapabilities(response, httptest.NewRequest("GET", "/scanners/runtime-capabilities", nil))
	if response.Code != 503 || strings.Contains(response.Body.String(), "private storage error") {
		t.Fatalf("unknown health: %d %s", response.Code, response.Body.String())
	}
	t.Setenv("WOLF_SCAN_EXECUTION_MODE", "inline")
	response = httptest.NewRecorder()
	RuntimeCapabilities(response, httptest.NewRequest("GET", "/scanners/runtime-capabilities", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"queue_execution":false`) {
		t.Fatalf("inline mode requires no worker: %d %s", response.Code, response.Body.String())
	}
}
