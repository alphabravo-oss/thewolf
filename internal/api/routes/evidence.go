package routes

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
)

// ListEvidence handles GET /evidence?vulnerability_id= and
// GET /vulnerabilities/{id}/evidence. Findings stay the compatibility object.
func ListEvidence(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil || h.Store == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		id = strings.TrimSpace(r.URL.Query().Get("vulnerability_id"))
	}
	if id == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "vulnerability_id is required")
		return
	}
	v, err := h.Store.GetVulnerabilityByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "vulnerability not found")
		return
	}
	if !vulnerabilityVisibleToCaller(w, r, h, v, claims) {
		return
	}
	attachVulnerabilityEvidence(r.Context(), h, v)
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: v.Evidence})
}
