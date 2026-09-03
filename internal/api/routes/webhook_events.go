package routes

import (
	"net/http"

	"github.com/alphabravocompany/thewolf/internal/api/response"
)

// ApplicationEvents is the Community named-event catalog (PRD §10.4).
func ApplicationEvents() []map[string]string {
	return []map[string]string{
		{"id": "scan.completed", "summary": "A scan reached a terminal state"},
		{"id": "scan.failed", "summary": "A scan failed or produced no tools"},
		{"id": "finding.created", "summary": "New current-open findings landed"},
		{"id": "vulnerability.updated", "summary": "Canonical vulnerability cluster changed"},
		{"id": "policy.evaluated", "summary": "A quality gate produced a decision"},
		{"id": "remediation.verified", "summary": "A fix job finished independent verification"},
		{"id": "plugin.failed", "summary": "A scanner plugin failed or was skipped"},
	}
}

func ListWebhookEvents(w http.ResponseWriter, r *http.Request) {
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: ApplicationEvents()})
}
