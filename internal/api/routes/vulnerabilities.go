package routes

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// ListVulnerabilities handles GET /vulnerabilities — canonical clusters dual-written
// from findings. Findings remain the compatibility object.
func ListVulnerabilities(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	q := r.URL.Query()
	page, perPage := parsePagination(r)
	fleet := fleetVisible(r.Context(), h.Store, claims.UserID)
	filter := db.VulnListQuery{
		UserID: claims.UserID,
		Fleet:  fleet,
		RepoID: q.Get("repo_id"),
		ScanID: q.Get("scan_id"),
		Search: q.Get("q"), SortBy: q.Get("sort"), Desc: !strings.EqualFold(q.Get("order"), "asc"),
		Limit:  perPage,
		Offset: (page - 1) * perPage,
	}
	vulns, total, err := h.Store.ListVulnerabilitiesPage(r.Context(), filter)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load vulnerabilities")
		return
	}
	needsBackfill := total == 0
	if needsBackfill && filter.Search != "" {
		unfiltered := filter
		unfiltered.Search, unfiltered.Offset, unfiltered.Limit = "", 0, 1
		_, existing, err := h.Store.ListVulnerabilitiesPage(r.Context(), unfiltered)
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load vulnerabilities")
			return
		}
		needsBackfill = existing == 0
	}
	if needsBackfill {
		if err := backfillVulnerabilities(r.Context(), h, claims.UserID, fleet, filter.RepoID, filter.ScanID); err != nil {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to reconcile vulnerabilities")
			return
		}
		vulns, total, err = h.Store.ListVulnerabilitiesPage(r.Context(), filter)
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load vulnerabilities")
			return
		}
	}
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: vulns,
		Meta: response.ListMeta{Total: total, Page: page, PerPage: perPage},
	})
}

// GetVulnerability handles GET /vulnerabilities/{id}. Cross-tenant misses 404.
func GetVulnerability(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	id := chi.URLParam(r, "id")
	v, err := h.Store.GetVulnerabilityByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "vulnerability not found")
		return
	}
	if !vulnerabilityVisibleToCaller(w, r, h, v, claims) {
		return
	}
	if err := attachVulnerabilityEvidence(r.Context(), h, v); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load evidence")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: v})
}

func attachVulnerabilityEvidence(ctx context.Context, h *Handler, v *models.Vulnerability) error {
	if h == nil || h.Store == nil || v == nil {
		return errors.New("vulnerability handler is not initialized")
	}
	ev, err := h.Store.ListEvidenceByVulnerability(ctx, v.ID)
	if err != nil {
		return err
	}
	v.Evidence = ev
	v.MergeReason = mergeReason(v)
	return nil
}

func mergeReason(v *models.Vulnerability) string {
	if v == nil {
		return ""
	}
	for _, e := range v.Evidence {
		if e.Reason == "manual merge" {
			return "Grouped manually by a user."
		}
		if e.Reason == "manual split" {
			return "Split manually by a user."
		}
	}
	if strings.HasPrefix(v.CanonicalKey, "split:") {
		return "Split out of a previous cluster."
	}
	n := len(v.FindingIDs)
	if n == 0 && len(v.Evidence) > 0 {
		n = len(v.Evidence)
	}
	if n <= 1 {
		return "Single finding; no merge."
	}
	return "Grouped because findings share the same identity key."
}

func SplitVulnerability(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	v, err := h.Store.GetVulnerabilityByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "vulnerability not found")
		return
	}
	if !vulnerabilityVisibleToCaller(w, r, h, v, claims) {
		return
	}
	var req struct {
		FindingIDs []string `json:"finding_ids"`
	}
	if !scannerDecode(w, r, &req) {
		return
	}
	if len(req.FindingIDs) == 0 {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "finding_ids is required")
		return
	}
	out, err := h.Store.SplitVulnerability(r.Context(), v.RepoID, v.ID, req.FindingIDs)
	if err != nil {
		writeClusterChangeError(w, err)
		return
	}
	out.MergeReason = mergeReason(out)
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: out})
}

func MergeVulnerability(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	target, err := h.Store.GetVulnerabilityByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "vulnerability not found")
		return
	}
	if !vulnerabilityVisibleToCaller(w, r, h, target, claims) {
		return
	}
	var req struct {
		VulnerabilityID string `json:"vulnerability_id"`
	}
	if !scannerDecode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.VulnerabilityID) == "" {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "vulnerability_id is required")
		return
	}
	if req.VulnerabilityID == target.ID {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "cannot merge a vulnerability into itself")
		return
	}
	src, err := h.Store.GetVulnerabilityByID(r.Context(), req.VulnerabilityID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "vulnerability not found")
		return
	}
	if !vulnerabilityVisibleToCaller(w, r, h, src, claims) {
		return
	}
	if src.RepoID != target.RepoID {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "can only merge vulnerabilities in the same repo")
		return
	}
	out, err := h.Store.MergeVulnerabilities(r.Context(), target.RepoID, src.ID, target.ID)
	if err != nil {
		writeClusterChangeError(w, err)
		return
	}
	out.MergeReason = mergeReason(out)
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: out})
}

func vulnerabilityVisibleToCaller(w http.ResponseWriter, r *http.Request, h *Handler, v *models.Vulnerability, claims *auth.Claims) bool {
	if fleetVisible(r.Context(), h.Store, claims.UserID) {
		return true
	}
	repo, err := h.Store.GetRepoByID(r.Context(), v.RepoID)
	if err == nil && canModifyOwned(claims, repo.UserID) {
		return true
	}
	response.WriteError(w, http.StatusNotFound, "not_found", "vulnerability not found")
	return false
}

func backfillVulnerabilities(ctx context.Context, h *Handler, userID string, fleet bool, repoID, scanID string) error {
	if h == nil || h.Store == nil {
		return errors.New("scan handler is not initialized")
	}
	findings, err := h.Store.ListCurrentOpenFindings(ctx, userID, fleet, "")
	if err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, f := range findings {
		if (repoID != "" && f.RepoID != repoID) || (scanID != "" && f.ScanID != scanID) {
			continue
		}
		if _, ok := seen[f.ScanID]; ok {
			continue
		}
		seen[f.ScanID] = struct{}{}
		if err := DualWriteVulnerabilities(ctx, h, &models.Scan{ID: f.ScanID, RepoID: f.RepoID}); err != nil {
			return err
		}
	}
	return nil
}

func DualWriteVulnerabilities(ctx context.Context, h *Handler, scan *models.Scan) error {
	if h == nil || h.Store == nil || scan == nil {
		return errors.New("scan handler and scan are required")
	}
	return h.Store.ReconcileVulnerabilities(ctx, scan.ID)
}

func writeClusterChangeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrInvalidClusterChange):
		response.WriteError(w, http.StatusBadRequest, "invalid_cluster_change", "select a nonempty proper subset of source evidence for split, or another cluster in the same repository for merge")
	case errors.Is(err, db.ErrClusterScanActive):
		response.WriteError(w, http.StatusConflict, "scan_active", "wait for every affected scan to finish before changing its clusters")
	case errors.Is(err, sql.ErrNoRows):
		response.WriteError(w, http.StatusNotFound, "not_found", "vulnerability or evidence no longer exists")
	default:
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to change cluster membership")
	}
}
