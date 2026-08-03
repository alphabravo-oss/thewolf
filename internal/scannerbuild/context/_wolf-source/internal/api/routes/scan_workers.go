package routes

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/models"
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
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("WOLF_SCAN_RUNTIME")))
	if backend == "" {
		backend = "docker"
	}
	dockerManagement := backend == "docker"
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]interface{}{
		"scan_runtime":            backend,
		"queue_execution":         queuedScanExecution(),
		"docker_image_management": dockerManagement,
		"scanner_jobs":            backend == "kubernetes",
		"durable_events":          true,
		"tool_cancellation":       true,
	}})
}
