package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/finding/identity"
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
	page := parseIntDefault(q.Get("page"), 1)
	perPage := parseIntDefault(q.Get("per_page"), 50)
	if perPage < 1 {
		perPage = 50
	}
	if perPage > 200 {
		perPage = 200
	}
	if page < 1 {
		page = 1
	}
	fleet := fleetVisible(r.Context(), h.Store, claims.UserID)
	filter := db.VulnListQuery{
		UserID: claims.UserID,
		Fleet:  fleet,
		RepoID: q.Get("repo_id"),
		ScanID: q.Get("scan_id"),
		Limit:  perPage,
		Offset: (page - 1) * perPage,
	}
	vulns, total, err := h.Store.ListVulnerabilitiesPage(r.Context(), filter)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load vulnerabilities")
		return
	}
	if total == 0 {
		backfillVulnerabilities(r.Context(), h, claims.UserID, fleet)
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
	attachVulnerabilityEvidence(r.Context(), h, v)
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: v})
}

func attachVulnerabilityEvidence(ctx context.Context, h *Handler, v *models.Vulnerability) {
	if h == nil || h.Store == nil || v == nil {
		return
	}
	ev, err := h.Store.ListEvidenceByVulnerability(ctx, v.ID)
	if err != nil {
		return
	}
	v.Evidence = ev
	v.MergeReason = mergeReason(v)
}

func mergeReason(v *models.Vulnerability) string {
	if v == nil {
		return ""
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.FindingIDs) == 0 {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "finding_ids is required")
		return
	}
	ev, err := h.Store.ListEvidenceByVulnerability(r.Context(), v.ID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load evidence")
		return
	}
	want := map[string]struct{}{}
	for _, id := range req.FindingIDs {
		want[id] = struct{}{}
	}
	var move []string
	for _, e := range ev {
		if _, ok := want[e.FindingID]; ok {
			move = append(move, e.ID)
		}
	}
	if len(move) == 0 || len(move) >= len(ev) {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "split must move some, not all, evidence")
		return
	}
	neu := *v
	neu.ID = uuid.NewString()
	neu.CanonicalKey = "split:" + neu.ID
	neu.FindingIDsJSON = ""
	neu.CorroboratedByJSON = ""
	neu.Evidence = nil
	if err := h.Store.UpsertVulnerability(r.Context(), &neu); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create split vulnerability")
		return
	}
	if err := h.Store.MoveVulnerabilityEvidence(r.Context(), move, neu.ID); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to move evidence")
		return
	}
	_ = h.Store.RefreshVulnerabilityEvidence(r.Context(), v.ID)
	_ = h.Store.RefreshVulnerabilityEvidence(r.Context(), neu.ID)
	out, err := h.Store.GetVulnerabilityByID(r.Context(), neu.ID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load split vulnerability")
		return
	}
	attachVulnerabilityEvidence(r.Context(), h, out)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.VulnerabilityID) == "" {
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
	ev, err := h.Store.ListEvidenceByVulnerability(r.Context(), src.ID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load evidence")
		return
	}
	ids := make([]string, 0, len(ev))
	for _, e := range ev {
		ids = append(ids, e.ID)
	}
	if err := h.Store.MoveVulnerabilityEvidence(r.Context(), ids, target.ID); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to move evidence")
		return
	}
	_ = h.Store.DeleteVulnerability(r.Context(), src.ID)
	_ = h.Store.RefreshVulnerabilityEvidence(r.Context(), target.ID)
	out, err := h.Store.GetVulnerabilityByID(r.Context(), target.ID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load merged vulnerability")
		return
	}
	attachVulnerabilityEvidence(r.Context(), h, out)
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

func backfillVulnerabilities(ctx context.Context, h *Handler, userID string, fleet bool) {
	if h == nil || h.Store == nil {
		return
	}
	findings, err := h.Store.ListCurrentOpenFindings(ctx, userID, fleet, "")
	if err != nil || len(findings) == 0 {
		return
	}
	seen := map[string]struct{}{}
	for _, f := range findings {
		if _, ok := seen[f.ScanID]; ok {
			continue
		}
		seen[f.ScanID] = struct{}{}
		DualWriteVulnerabilities(ctx, h, &models.Scan{ID: f.ScanID, RepoID: f.RepoID})
	}
}

func DualWriteVulnerabilities(ctx context.Context, h *Handler, scan *models.Scan) {
	if h == nil || h.Store == nil || scan == nil {
		return
	}
	findings, err := h.Store.ListFindingsByScan(ctx, scan.ID)
	if err != nil || len(findings) == 0 {
		return
	}
	grouped := map[string]*models.Vulnerability{}
	order := make([]string, 0, len(findings))
	for i := range findings {
		f := findings[i]
		key := canonicalVulnerabilityKey(f)
		if key == "" {
			continue
		}
		existing, ok := grouped[key]
		if !ok {
			v := vulnerabilityFromFinding(scan, f, key)
			grouped[key] = v
			order = append(order, key)
			continue
		}
		existing.FindingIDs = appendUnique(existing.FindingIDs, f.ID)
		existing.CorroboratedBy = appendUnique(existing.CorroboratedBy, f.CorroboratedBy...)
		existing.EvidenceCount = len(existing.FindingIDs)
		if f.CompositeScore > existing.CompositeScore {
			existing.CompositeScore = f.CompositeScore
			existing.Severity = f.Severity
			existing.Title = f.Title
		}
		existing.FindingIDsJSON = ""
		existing.CorroboratedByJSON = ""
	}
	for _, key := range order {
		g := grouped[key]
		_ = h.Store.UpsertVulnerability(ctx, g)
		stored, err := h.Store.GetVulnerabilityByRepoKey(ctx, g.RepoID, g.CanonicalKey)
		if err != nil {
			continue
		}
		reason := "same identity"
		for _, fid := range g.FindingIDs {
			var f models.Finding
			for i := range findings {
				if findings[i].ID == fid {
					f = findings[i]
					break
				}
			}
			_ = h.Store.InsertVulnerabilityEvidence(ctx, &models.VulnerabilityEvidence{
				ID:              uuid.NewString(),
				VulnerabilityID: stored.ID,
				FindingID:       fid,
				ToolName:        f.ToolName,
				Title:           f.Title,
				FilePath:        f.FilePath,
				LineStart:       f.LineStart,
				RuleID:          f.RuleID,
				Reason:          reason,
			})
		}
		_ = h.Store.RefreshVulnerabilityEvidence(ctx, stored.ID)
	}
}

func canonicalVulnerabilityKey(f models.Finding) string {
	if s := strings.TrimSpace(f.StableFingerprint); s != "" {
		return s
	}
	if s := strings.TrimSpace(f.Fingerprint); s != "" {
		return s
	}
	return identity.Build(f).Stable
}

func vulnerabilityFromFinding(scan *models.Scan, f models.Finding, key string) *models.Vulnerability {
	tools := f.CorroboratedBy
	if len(tools) == 0 && f.ToolName != "" {
		tools = []string{f.ToolName}
	}
	ids := []string{f.ID}
	count := len(tools)
	if count < 1 {
		count = 1
	}
	return &models.Vulnerability{
		ID:             uuid.NewString(),
		RepoID:         f.RepoID,
		ScanID:         scan.ID,
		CanonicalKey:   key,
		Title:          f.Title,
		Severity:       f.Severity,
		Category:       f.Category,
		FineCategory:   f.FineCategory,
		Confidence:     f.Confidence,
		BaselineState:  f.BaselineState,
		CompositeScore: f.CompositeScore,
		EvidenceCount:  count,
		FindingIDs:     ids,
		CorroboratedBy: tools,
		Suppressed:     f.Suppressed,
	}
}

func appendUnique(dst []string, extra ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(extra))
	for _, s := range dst {
		seen[s] = struct{}{}
	}
	for _, s := range extra {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		dst = append(dst, s)
	}
	return dst
}
