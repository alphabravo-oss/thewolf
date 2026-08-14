package routes

import (
	"net/http"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// ListOrphanScans handles GET /api/scans/orphans — leftover scans and
// findings whose repo (or scan) is gone.
func ListOrphanScans(w http.ResponseWriter, r *http.Request) {
	if auth.GetUserFromContext(r.Context()) == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	sum, err := h.Store.OrphanSummary(r.Context())
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list leftover records")
		return
	}
	if sum.ScanIDs == nil {
		sum.ScanIDs = []string{}
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{
		"scan_ids":      sum.ScanIDs,
		"count":         sum.ScanCount + sum.FindingCount,
		"scan_count":    sum.ScanCount,
		"finding_count": sum.FindingCount,
	}})
}

// PurgeOrphanScans handles DELETE /api/scans/orphans — remove leftover
// scan/finding/fix records whose repo was deleted.
func PurgeOrphanScans(w http.ResponseWriter, r *http.Request) {
	if auth.GetUserFromContext(r.Context()) == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	before, _ := h.Store.OrphanSummary(r.Context())
	ids, err := h.Store.PurgeOrphanedRecords(r.Context())
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to purge leftover records")
		return
	}
	if len(ids) > 0 && artifacts.Global != nil {
		go artifacts.Global.DeleteScans(ids)
	}
	if ids == nil {
		ids = []string{}
	}
	findings := 0
	if before != nil {
		findings = before.FindingCount
	}
	wolflog.Info().Int("scans_deleted", len(ids)).Int("findings", findings).Msg("purged leftover records for deleted repos")
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{
		"scan_ids":      ids,
		"purged":        len(ids) + findings,
		"scan_count":    len(ids),
		"finding_count": findings,
	}})
}
