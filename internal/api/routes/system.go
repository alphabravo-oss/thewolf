package routes

import (
	"net/http"
	"time"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

var startTime = time.Now()

// Version info set via ldflags
var (
	AppVersion  = "0.2.0"
	BuildCommit = "dev"
	BuildDate   = "unknown"
)

// Health always returns 200 if the server process is alive. This is the
// liveness probe — it does not verify downstream dependencies.
func Health(w http.ResponseWriter, r *http.Request) {
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"status":    "ok",
			"uptime_ms": time.Since(startTime).Milliseconds(),
		},
	})
}

// Ready returns 200 only when the database is reachable and migrations have
// been applied. Use this as a Kubernetes readiness probe.
func Ready(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil || h.Store == nil {
		wolflog.Warn().Msg("readiness check failed: handler or store not initialized")
		response.WriteError(w, http.StatusServiceUnavailable, "not_ready", "store not initialized")
		return
	}

	if err := h.Store.Ping(r.Context()); err != nil {
		wolflog.Error().Err(err).Msg("readiness check failed: database ping error")
		response.WriteError(w, http.StatusServiceUnavailable, "not_ready", "database unavailable")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"status":   "ready",
			"database": "ok",
		},
	})
}

func Version(w http.ResponseWriter, r *http.Request) {
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]string{
			"version": AppVersion,
			"commit":  BuildCommit,
			"date":    BuildDate,
			"name":    "the-wolf",
		},
	})
}
