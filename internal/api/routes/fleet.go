package routes

import (
	"context"
	"net/http"
	"strconv"

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

// FleetNeedsAttention handles GET /fleet/needs-attention — the top-N repos
// scored by a composite "needs attention" formula.
func FleetNeedsAttention(w http.ResponseWriter, r *http.Request) {
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

	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	rows, err := h.Store.FleetNeedsAttention(r.Context(), claims.UserID, fleetModeEnabled(r.Context(), h.Store), limit)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "compute needs-attention: "+err.Error())
		return
	}
	if rows == nil {
		rows = []db.NeedsAttentionRow{}
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: rows})
}

// FindingsAggregate handles GET /findings/aggregate?group_by=rule_id&limit=N
// — top-N rule_ids by distinct repo count, then by total finding count.
func FindingsAggregate(w http.ResponseWriter, r *http.Request) {
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

	groupBy := r.URL.Query().Get("group_by")
	if groupBy == "" {
		groupBy = "rule_id"
	}
	if groupBy != "rule_id" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "group_by must be 'rule_id'")
		return
	}

	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	rows, err := h.Store.FindingsAggregateByRule(r.Context(), claims.UserID, fleetModeEnabled(r.Context(), h.Store), limit)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "aggregate findings: "+err.Error())
		return
	}
	if rows == nil {
		rows = []db.FindingsAggregateRow{}
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: rows})
}
