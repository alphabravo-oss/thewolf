package routes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/finding/suppression"
	"github.com/alphabravocompany/thewolf/internal/models"
)

type suppressionRequest struct {
	FindingID  string `json:"finding_id,omitempty"`
	RepoID     string `json:"repo_id,omitempty"`
	ScopeType  string `json:"scope_type,omitempty"`
	ScopeValue string `json:"scope_value,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Reason     string `json:"reason"`
	ExpiresAt  string `json:"expires_at,omitempty"`
}

type suppressionPreviewResponse struct {
	Count    int              `json:"count"`
	Findings []models.Finding `json:"findings"`
}

func ListSuppressions(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	repoID := r.URL.Query().Get("repo_id")
	if repoID == "" {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "repo_id is required")
		return
	}
	if !repoBelongsToUser(w, r, h, repoID, claims.UserID) {
		return
	}
	includeInactive := r.URL.Query().Get("include_inactive") == "true"
	suppressions, err := h.Store.ListFindingSuppressions(r.Context(), repoID, includeInactive)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list suppressions")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: suppressions,
		Meta: response.ListMeta{Total: len(suppressions), Page: 1, PerPage: len(suppressions)},
	})
}

func CreateSuppression(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	req, expiresAt, ok := decodeSuppressionRequest(w, r)
	if !ok {
		return
	}
	s, ok := buildSuppressionFromRequest(w, r, h, claims.UserID, req, expiresAt)
	if !ok {
		return
	}
	if err := h.Store.CreateFindingSuppression(r.Context(), s); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create suppression")
		return
	}
	_ = h.Store.CreateFindingSuppressionAudit(r.Context(), &models.FindingSuppressionAudit{
		ID:            uuid.New().String(),
		SuppressionID: s.ID,
		Action:        "created",
		ActorID:       claims.UserID,
		DetailsJSON:   "{}",
	})
	applyOneSuppressionToExistingFindings(r, h, *s)

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: s})
}

func PreviewSuppression(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	req, expiresAt, ok := decodeSuppressionRequest(w, r)
	if !ok {
		return
	}
	s, ok := buildSuppressionFromRequest(w, r, h, claims.UserID, req, expiresAt)
	if !ok {
		return
	}
	findings, branches, err := repoFindingsAndBranches(r, h, s.RepoID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load findings")
		return
	}
	matched := make([]models.Finding, 0)
	now := time.Now().UTC()
	for _, f := range findings {
		if suppression.Matches(*s, f, branches[f.ScanID], now) {
			matched = append(matched, f)
		}
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: suppressionPreviewResponse{Count: len(matched), Findings: matched},
	})
}

func RevokeSuppression(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	s, err := h.Store.GetFindingSuppressionByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "suppression not found")
		return
	}
	if !repoBelongsToUser(w, r, h, s.RepoID, claims.UserID) {
		return
	}
	if err := h.Store.RevokeFindingSuppression(r.Context(), id); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to revoke suppression")
		return
	}
	_ = h.Store.CreateFindingSuppressionAudit(r.Context(), &models.FindingSuppressionAudit{
		ID:            uuid.New().String(),
		SuppressionID: id,
		Action:        "revoked",
		ActorID:       claims.UserID,
		DetailsJSON:   "{}",
	})
	clearSuppressionFromExistingFindings(r, h, *s)
	updated, _ := h.Store.GetFindingSuppressionByID(r.Context(), id)
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: updated})
}

func decodeSuppressionRequest(w http.ResponseWriter, r *http.Request) (suppressionRequest, *time.Time, bool) {
	var req suppressionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return req, nil, false
	}
	if req.Reason == "" {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "reason is required")
		return req, nil, false
	}
	var expiresAt *time.Time
	if req.ExpiresAt != "" {
		ts, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			response.WriteError(w, http.StatusBadRequest, "bad_request", "expires_at must be RFC3339")
			return req, nil, false
		}
		expiresAt = &ts
	}
	return req, expiresAt, true
}

func buildSuppressionFromRequest(w http.ResponseWriter, r *http.Request, h *Handler, userID string, req suppressionRequest, expiresAt *time.Time) (*models.FindingSuppression, bool) {
	repoID := req.RepoID
	scopeType := models.SuppressionScopeType(req.ScopeType)
	scopeValue := req.ScopeValue

	if req.FindingID != "" {
		f, err := h.Store.GetFindingByID(r.Context(), req.FindingID)
		if err != nil {
			response.WriteError(w, http.StatusNotFound, "not_found", "finding not found")
			return nil, false
		}
		repoID = f.RepoID
		scopeType = models.SuppressionScopeStableFingerprint
		scopeValue = f.StableFingerprint
		if scopeValue == "" {
			scopeValue = f.Fingerprint
			scopeType = models.SuppressionScopeFingerprint
		}
	}

	if repoID == "" || scopeType == "" || scopeValue == "" {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "repo_id, scope_type, and scope_value are required unless finding_id is provided")
		return nil, false
	}
	if !isValidSuppressionScope(scopeType) {
		response.WriteError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("invalid scope_type %q", scopeType))
		return nil, false
	}
	if !repoBelongsToUser(w, r, h, repoID, userID) {
		return nil, false
	}

	return &models.FindingSuppression{
		ID:         uuid.New().String(),
		RepoID:     repoID,
		CreatedBy:  userID,
		ScopeType:  scopeType,
		ScopeValue: scopeValue,
		Branch:     req.Branch,
		Reason:     req.Reason,
		ExpiresAt:  expiresAt,
		Status:     models.SuppressionStatusActive,
	}, true
}

func isValidSuppressionScope(scope models.SuppressionScopeType) bool {
	switch scope {
	case models.SuppressionScopeFingerprint,
		models.SuppressionScopeStableFingerprint,
		models.SuppressionScopeRule,
		models.SuppressionScopeFineCategory,
		models.SuppressionScopePathGlob:
		return true
	default:
		return false
	}
}

func repoBelongsToUser(w http.ResponseWriter, r *http.Request, h *Handler, repoID, userID string) bool {
	repo, err := h.Store.GetRepoByID(r.Context(), repoID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "repo not found")
		return false
	}
	if repo.UserID != userID {
		response.WriteError(w, http.StatusForbidden, "forbidden", "repo does not belong to current user")
		return false
	}
	return true
}

func repoFindingsAndBranches(r *http.Request, h *Handler, repoID string) ([]models.Finding, map[string]string, error) {
	findings, err := h.Store.ListFindingsByRepo(r.Context(), repoID)
	if err != nil {
		return nil, nil, err
	}
	scans, err := h.Store.ListScansByRepo(r.Context(), repoID)
	if err != nil {
		return nil, nil, err
	}
	branches := make(map[string]string, len(scans))
	for _, scan := range scans {
		branches[scan.ID] = scan.Branch
	}
	return findings, branches, nil
}

func applyOneSuppressionToExistingFindings(r *http.Request, h *Handler, s models.FindingSuppression) {
	findings, branches, err := repoFindingsAndBranches(r, h, s.RepoID)
	if err != nil {
		return
	}
	out, _ := suppression.Apply(findings, []models.FindingSuppression{s}, branches, time.Now().UTC())
	for i := range out {
		if out[i].SuppressionID == s.ID {
			_ = h.Store.UpdateFinding(r.Context(), &out[i])
		}
	}
}

func clearSuppressionFromExistingFindings(r *http.Request, h *Handler, s models.FindingSuppression) {
	findings, _, err := repoFindingsAndBranches(r, h, s.RepoID)
	if err != nil {
		return
	}
	for i := range findings {
		if findings[i].SuppressionID != s.ID {
			continue
		}
		findings[i].Suppressed = false
		findings[i].SuppressionID = ""
		findings[i].SuppressedReason = ""
		_ = h.Store.UpdateFinding(r.Context(), &findings[i])
	}
}
