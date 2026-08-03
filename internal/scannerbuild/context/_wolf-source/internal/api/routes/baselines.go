package routes

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
)

type createBaselineRequest struct {
	Name     string `json:"name"`
	Branch   string `json:"branch,omitempty"`
	ScanID   string `json:"scan_id"`
	Strategy string `json:"strategy,omitempty"`
}

func ListRepoBaselines(w http.ResponseWriter, r *http.Request) {
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

	repoID := chi.URLParam(r, "id")
	repo, err := h.Store.GetRepoByID(r.Context(), repoID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	if repo.UserID != claims.UserID {
		response.WriteError(w, http.StatusForbidden, "forbidden", "repo does not belong to current user")
		return
	}

	baselines, err := h.Store.ListScanBaselines(r.Context(), repoID, r.URL.Query().Get("branch"))
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list baselines")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: baselines,
		Meta: response.ListMeta{Total: len(baselines), Page: 1, PerPage: len(baselines)},
	})
}

func CreateRepoBaseline(w http.ResponseWriter, r *http.Request) {
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

	repoID := chi.URLParam(r, "id")
	repo, err := h.Store.GetRepoByID(r.Context(), repoID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	if repo.UserID != claims.UserID {
		response.WriteError(w, http.StatusForbidden, "forbidden", "repo does not belong to current user")
		return
	}

	var req createBaselineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Name == "" || req.ScanID == "" {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "name and scan_id are required")
		return
	}

	scan, err := h.Store.GetScanByID(r.Context(), req.ScanID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "scan not found")
		return
	}
	if scan.UserID != claims.UserID || scan.RepoID != repoID {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "scan does not belong to this repo")
		return
	}
	branch := req.Branch
	if branch == "" {
		branch = scan.Branch
	}

	baseline := &models.ScanBaseline{
		ID:        uuid.New().String(),
		RepoID:    repoID,
		Branch:    branch,
		Name:      req.Name,
		ScanID:    scan.ID,
		Strategy:  req.Strategy,
		CreatedBy: claims.UserID,
	}
	if err := h.Store.CreateScanBaseline(r.Context(), baseline); err != nil {
		response.WriteError(w, http.StatusConflict, "conflict", "baseline already exists or could not be created")
		return
	}

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: baseline})
}
