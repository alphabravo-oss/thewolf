package routes

import "net/http"

import "github.com/alphabravocompany/thewolf/internal/api/response"

// ListScanProfiles handles GET /scan-profiles — Fast/Standard/Release names
// the start dialog and CLI --profile already use.
func ListScanProfiles(w http.ResponseWriter, r *http.Request) {
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: []map[string]any{
			{"id": "fast", "label": "Fast PR", "summary": "Skip heavy tools after the first completed scan."},
			{"id": "pr", "label": "PR", "summary": "Same as Fast; used by inbound GitHub webhooks."},
			{"id": "standard", "label": "Standard", "summary": "Default planned tool set for the repo."},
			{"id": "release", "label": "Release", "summary": "Standard plus release-oriented completeness."},
			{"id": "full", "label": "Full", "summary": "Every applicable non-DAST scanner."},
			{"id": "targeted", "label": "Targeted", "summary": "Requires tools, categories, or include_paths."},
		},
	})
}
