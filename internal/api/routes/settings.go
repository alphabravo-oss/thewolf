package routes

import (
	"encoding/json"
	"net/http"

	"github.com/alphabravocompany/thewolf/internal/api/response"
)

// ListSettings returns all key/value settings.
// GET /api/settings
func ListSettings(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	settings, err := h.Store.ListSettings(r.Context())
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list settings")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: settings})
}

// UpdateSettings bulk-updates settings from a JSON map of key/value pairs.
// PUT /api/settings
func UpdateSettings(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	var updates map[string]string
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	if len(updates) == 0 {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "no settings provided")
		return
	}

	ctx := r.Context()
	for key, value := range updates {
		if err := h.Store.SetSetting(ctx, key, value); err != nil {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to update setting: "+key)
			return
		}
	}

	// Return the full settings map after update.
	settings, err := h.Store.ListSettings(ctx)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to read settings after update")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: settings})
}
