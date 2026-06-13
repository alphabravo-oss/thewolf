package routes

import (
	"context"
	"net/http"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
)

// fleetModeEnabled reads the fleet_mode setting; on error or absence, false.
func fleetModeEnabled(ctx context.Context, store db.Store) bool {
	v, err := store.GetSetting(ctx, "fleet_mode")
	return err == nil && v == "true"
}

// FleetPosture handles GET /fleet/posture — aggregates open findings by
// severity, week-over-week deltas, fleet repo count, and gate-failure count.
func FleetPosture(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	posture, err := h.Store.FleetPosture(r.Context(), claims.UserID, fleetModeEnabled(r.Context(), h.Store))
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "compute posture: "+err.Error())
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: posture})
}

// FleetInventory handles GET /fleet/inventory — repo counts grouped by
// source_type, collection name, and detected language.
func FleetInventory(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	inv, err := h.Store.FleetInventory(r.Context(), claims.UserID, fleetModeEnabled(r.Context(), h.Store))
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "compute inventory: "+err.Error())
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: inv})
}
