package routes

import (
	"context"
	"net/http"
	"time"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

var startTime = time.Now()

// Version info set via ldflags
var (
	AppVersion  = "0.2.0"
	BuildCommit = "dev"
	BuildDate   = "unknown"
)

// Health always returns 200 if the server process is alive. Dependency and
// release-component state is diagnostic and does not change liveness.
func Health(w http.ResponseWriter, r *http.Request) {
	releaseFactory := scannerobservability.Default.Snapshot(r.Context())
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"status":                 "ok",
			"uptime_ms":              time.Since(startTime).Milliseconds(),
			"release_factory":        releaseFactory,
			"scanner_release_alerts": scannerReleaseAlertHealth(r.Context()),
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
	maintenance, err := h.Store.ScannerReleases().GetReleaseMaintenanceStatus(r.Context())
	if err != nil {
		wolflog.Error().Err(err).Msg("readiness check failed: release maintenance unavailable")
		response.WriteError(w, http.StatusServiceUnavailable, "not_ready", "release maintenance unavailable")
		return
	}
	if maintenance.RestoreActive(time.Now()) {
		wolflog.Warn().Str("owner", maintenance.Owner).Msg("readiness disabled during scanner release restore")
		response.WriteError(w, http.StatusServiceUnavailable, "not_ready", "scanner release restore in progress")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"status":                 "ready",
			"database":               "ok",
			"maintenance":            "normal",
			"release_factory":        scannerobservability.Default.Snapshot(r.Context()),
			"scanner_release_alerts": scannerReleaseAlertHealth(r.Context()),
		},
	})
}

func scannerReleaseAlertHealth(ctx context.Context) map[string]any {
	result := map[string]any{"status": "unavailable"}
	if DefaultHandler == nil || DefaultHandler.Store == nil {
		return result
	}
	// Liveness must not wait behind the exclusive release-table locks used by
	// restore. Alert detail is diagnostic, so a bounded unavailable result is
	// preferable to blocking /health.
	checkContext, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	counts, err := DefaultHandler.Store.ScannerReleases().AlertCounts(checkContext)
	if err != nil {
		return result
	}
	status := "healthy"
	if counts.OpenCritical > 0 {
		status = "critical"
	} else if counts.OpenWarning > 0 {
		status = "warning"
	}
	return map[string]any{"status": status, "counts": counts}
}

func Metrics(w http.ResponseWriter, r *http.Request) {
	scannerobservability.Default.MetricsHandler().ServeHTTP(w, r)
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
