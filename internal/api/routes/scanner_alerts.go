package routes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func ScannerSupplyChainListAlerts(w http.ResponseWriter, r *http.Request) {
	filter, ok := scannerAlertFilter(w, r)
	if !ok {
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	page, err := store.ListAlerts(r.Context(), filter, scannerPage(r))
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "invalid_cursor", err.Error())
		return
	}
	items := make([]any, 0, len(page.Items))
	for index := range page.Items {
		items = append(items, scannerAlertView(&page.Items[index]))
	}
	w.Header().Set("Cache-Control", "no-store")
	response.WriteJSON(w, http.StatusOK, scannerCursorResponse{
		Data: items, Meta: scannerCursorMeta{NextCursor: page.NextCursor},
	})
}

func ScannerSupplyChainGetAlert(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	alert, err := store.GetAlert(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, alert.Version))
	w.Header().Set("Cache-Control", "no-store")
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: scannerAlertView(alert),
	})
}

func scannerAlertFilter(
	w http.ResponseWriter,
	r *http.Request,
) (scannerrelease.AlertFilter, bool) {
	stateText := strings.TrimSpace(r.URL.Query().Get("state"))
	filter := scannerrelease.AlertFilter{
		State: scannerrelease.AlertState(stateText),
		Kind: scannerrelease.AlertKind(
			strings.TrimSpace(r.URL.Query().Get("kind")),
		),
		Severity: scannerrelease.AlertSeverity(
			strings.TrimSpace(r.URL.Query().Get("severity")),
		),
	}
	if stateText == "" {
		filter.State = scannerrelease.AlertOpen
	} else if stateText == "all" {
		filter.State = ""
	} else if filter.State != scannerrelease.AlertOpen &&
		filter.State != scannerrelease.AlertResolved {
		response.WriteError(w, http.StatusBadRequest, "invalid_filter", "unsupported alert state")
		return scannerrelease.AlertFilter{}, false
	}
	switch filter.Kind {
	case "", scannerrelease.AlertMissedDiscovery,
		scannerrelease.AlertStaleStableRelease,
		scannerrelease.AlertQueueBacklog,
		scannerrelease.AlertLeaseChurn,
		scannerrelease.AlertRepeatedGateFailure,
		scannerrelease.AlertMirrorDrift,
		scannerrelease.AlertRolloutFailure,
		scannerrelease.AlertSignatureHealth:
	default:
		response.WriteError(w, http.StatusBadRequest, "invalid_filter", "unsupported alert kind")
		return scannerrelease.AlertFilter{}, false
	}
	switch filter.Severity {
	case "", scannerrelease.AlertWarning, scannerrelease.AlertCritical:
	default:
		response.WriteError(w, http.StatusBadRequest, "invalid_filter", "unsupported alert severity")
		return scannerrelease.AlertFilter{}, false
	}
	return filter, true
}

func scannerAlertView(alert *scannerrelease.Alert) map[string]any {
	view := map[string]any{
		"id": alert.ID, "fingerprint": alert.Fingerprint,
		"kind": alert.Kind, "severity": alert.Severity, "state": alert.State,
		"scope_type": alert.ScopeType, "scope_id": alert.ScopeID,
		"summary": alert.Summary, "policy_id": alert.PolicyID,
		"policy_scope":    alert.PolicyScope,
		"policy_revision": alert.PolicyRevision,
		"trigger_count":   alert.TriggerCount, "generation": alert.Generation,
		"version": alert.Version, "first_triggered_at": alert.FirstTriggeredAt,
		"last_triggered_at": alert.LastTriggeredAt, "resolved_at": alert.ResolvedAt,
		"created_at": alert.CreatedAt, "updated_at": alert.UpdatedAt,
	}
	var evidence any
	if json.Unmarshal([]byte(alert.EvidenceJSON), &evidence) == nil {
		view["evidence"] = evidence
	} else {
		view["evidence"] = map[string]any{}
	}
	return view
}
