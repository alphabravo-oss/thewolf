package routes

import (
	"net/http"
	"time"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/setup/scanners"
)

func ListScanWorkers(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	workers, err := h.Store.ListScanWorkers(r.Context(), time.Now().UTC().Add(-time.Minute))
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list scan workers")
		return
	}
	if workers == nil {
		workers = []models.ScanWorker{}
	}
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: workers,
		Meta: response.ListMeta{Total: len(workers), Page: 1, PerPage: len(workers)},
	})
}

func RuntimeCapabilities(w http.ResponseWriter, r *http.Request) {
	backend := scanners.RuntimeFromEnv()
	queued := queuedScanExecution()
	var readiness *scanWorkerReadiness
	if queued {
		h := DefaultHandler
		if h == nil || h.Store == nil {
			response.WriteError(w, http.StatusServiceUnavailable, "worker_health_unknown", "scan worker health is unavailable")
			return
		}
		workers, err := h.Store.ListScanWorkers(r.Context(), time.Time{})
		if err != nil {
			response.WriteError(w, http.StatusServiceUnavailable, "worker_health_unknown", "scan worker health is unavailable")
			return
		}
		readiness = workerReadiness(workers, backend, time.Now().UTC())
	}
	dockerManagement := backend == "docker"
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]interface{}{
		"scan_runtime":            backend,
		"queue_execution":         queued,
		"worker_readiness":        readiness,
		"docker_image_management": dockerManagement,
		"scanner_jobs":            backend == "kubernetes",
		"durable_events":          true,
		"tool_cancellation":       true,
	}})
}

type scanWorkerReadiness struct {
	Status              string     `json:"status"`
	ActiveWorkers       int        `json:"active_workers"`
	AvailableSlots      int        `json:"available_slots"`
	LastActiveHeartbeat *time.Time `json:"last_active_heartbeat,omitempty"`
}

func workerReadiness(workers []models.ScanWorker, backend string, now time.Time) *scanWorkerReadiness {
	result := &scanWorkerReadiness{Status: "unavailable"}
	for _, worker := range workers {
		if worker.Backend != backend || (worker.Status != "ready" && worker.Status != "busy") || worker.HeartbeatAt.After(now.Add(time.Minute)) {
			continue
		}
		if result.LastActiveHeartbeat == nil || worker.HeartbeatAt.After(*result.LastActiveHeartbeat) {
			heartbeat := worker.HeartbeatAt
			result.LastActiveHeartbeat = &heartbeat
		}
		if worker.HeartbeatAt.Before(now.Add(-time.Minute)) || worker.Capacity <= 0 {
			continue
		}
		result.ActiveWorkers++
		if worker.Status == "ready" {
			result.AvailableSlots += max(0, worker.Capacity-worker.ActiveScans)
		}
	}
	if result.ActiveWorkers > 0 {
		result.Status = "busy"
	}
	if result.AvailableSlots > 0 {
		result.Status = "ready"
	}
	return result
}
