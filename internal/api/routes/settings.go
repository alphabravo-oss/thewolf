package routes

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
)

// GetScheduling describes the next scan's effective limits on this daemon.
func GetScheduling(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, 500, "server_error", "handler not initialized")
		return
	}
	settings, err := h.Store.ListSettings(r.Context())
	if err != nil {
		response.WriteError(w, 500, "server_error", "failed to list settings")
		return
	}
	concurrency, _ := strconv.Atoi(settings["scan_concurrency"])
	heavy, _ := strconv.Atoi(settings["heavy_scanner_concurrency"])
	network, _ := strconv.Atoi(settings["network_scanner_concurrency"])
	plan := runner.DetectScheduling(r.Context(), container.Default(), concurrency, heavy, network)
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: plan})
}

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
	for _, key := range []string{"scan_concurrency", "heavy_scanner_concurrency", "network_scanner_concurrency"} {
		if value, ok := updates[key]; ok && value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || n > 32 {
				response.WriteError(w, http.StatusBadRequest, "validation_error", key+" must be 0 (automatic) or an integer from 1 to 32")
				return
			}
		}
	}

	if value, ok := updates[mfaRequiredSetting]; ok {
		required, err := parseMFARequired(value)
		if err != nil {
			response.WriteError(w, http.StatusBadRequest, "validation_error", "mfa_required must be a boolean")
			return
		}
		updates[mfaRequiredSetting] = strconv.FormatBool(required)
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
