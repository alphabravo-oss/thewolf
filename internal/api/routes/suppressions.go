package routes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/finding/identity"
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
	HiddenCount          int              `json:"hidden_count"`
	MatchedEvidenceCount int              `json:"matched_evidence_count"`
	ScanID               string           `json:"scan_id,omitempty"`
	Branch               string           `json:"branch,omitempty"`
	Count                int              `json:"count"`
	Findings             []models.Finding `json:"findings"`
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
	scan, findings, err := latestSuppressionPreview(r, h, *s)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load findings")
		return
	}
	before := append([]models.Finding(nil), findings...)
	after := append([]models.Finding(nil), findings...)
	now := time.Now().UTC()
	suppression.Apply(after, []models.FindingSuppression{*s}, map[string]string{scan.ID: scan.Branch}, now)
	// Match both projections in one batch so they use the same parsed rule file.
	projected := append(before, after...)
	if err := applyPathSuppressions(r.Context(), h, s.RepoID, projected); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "suppression_error", "failed to evaluate suppression rules")
		return
	}
	before, after = projected[:len(findings)], projected[len(findings):]
	oldPrimaries, newPrimaries := map[string]models.Finding{}, map[string]models.Finding{}
	matchedRoots := map[string]bool{}
	var order []string
	matchedEvidence := 0
	// Both views retain persisted groups, including manual groups. Never dedupe
	// again by native identity just to predict a rule's effect on existing issues.
	for i, f := range findings {
		root := f.ClusterID
		if root == "" {
			root = f.ID
		}
		if _, exists := oldPrimaries[root]; !exists {
			oldPrimaries[root] = before[i]
			newPrimaries[root] = after[i]
			order = append(order, root)
		}
		if f.ID == root {
			oldPrimaries[root] = before[i]
			newPrimaries[root] = after[i]
		}
		if suppression.Matches(*s, f, scan.Branch, now) {
			matchedRoots[root] = true
			matchedEvidence++
		}
	}
	for i, f := range findings {
		root := f.ClusterID
		if root == "" {
			root = f.ID
		}
		if identity.PreferPrimary(before[i], oldPrimaries[root]) {
			oldPrimaries[root] = before[i]
		}
		if identity.PreferPrimary(after[i], newPrimaries[root]) {
			newPrimaries[root] = after[i]
		}
	}
	matched := make([]models.Finding, 0, len(matchedRoots))
	hidden := 0
	for _, root := range order {
		if !matchedRoots[root] {
			continue
		}
		matched = append(matched, oldPrimaries[root])
		if !oldPrimaries[root].Suppressed && newPrimaries[root].Suppressed {
			hidden++
		}
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: suppressionPreviewResponse{ScanID: scan.ID, Branch: scan.Branch, Count: len(matched), Findings: matched, HiddenCount: hidden, MatchedEvidenceCount: matchedEvidence}})
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
	if err := h.Store.RevokeFindingSuppression(r.Context(), id, claims.UserID); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to revoke suppression")
		return
	}
	updated, err := h.Store.GetFindingSuppressionByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to retrieve suppression")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: updated})
}

func decodeSuppressionRequest(w http.ResponseWriter, r *http.Request) (suppressionRequest, *time.Time, bool) {
	var req suppressionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return req, nil, false
	}
	req.Reason = strings.TrimSpace(req.Reason)
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
		if !ts.After(time.Now()) {
			response.WriteError(w, http.StatusBadRequest, "bad_request", "expires_at must be in the future")
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

// Preview the newest observation in the requested branch (or repository), not
// the historical fanout that a durable rule may affect. Return its identity so
// clients can label the scope, including an empty or still-running latest scan.
func latestSuppressionPreview(r *http.Request, h *Handler, rule models.FindingSuppression) (models.Scan, []models.Finding, error) {
	scans, err := h.Store.ListScansByRepo(r.Context(), rule.RepoID)
	if err != nil {
		return models.Scan{}, nil, err
	}
	var latest models.Scan
	for _, scan := range scans {
		if rule.Branch != "" && rule.Branch != scan.Branch {
			continue
		}
		if latest.ID == "" || scan.CreatedAt.After(latest.CreatedAt) || (scan.CreatedAt.Equal(latest.CreatedAt) && scan.ID > latest.ID) {
			latest = scan
		}
	}
	if latest.ID == "" {
		return latest, nil, nil
	}
	findings, err := h.Store.ListFindingEvidenceByScan(r.Context(), latest.ID)
	return latest, findings, err
}
