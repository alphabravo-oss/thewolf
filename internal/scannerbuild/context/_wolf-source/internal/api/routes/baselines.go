package routes

import (
	"encoding/json"
	"errors"
	"github.com/alphabravocompany/thewolf/internal/db"
	"net/http"
	"strings"

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
	if _, ok := loadRepoForCaller(w, r, h.Store, repoID, claims); !ok {
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
	if _, ok := loadRepoForCaller(w, r, h.Store, repoID, claims); !ok {
		return
	}

	var req createBaselineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || req.ScanID == "" {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "name and scan_id are required")
		return
	}

	scan, ok := loadScanForCaller(w, r, h.Store, req.ScanID, claims)
	if !ok {
		return
	}
	if scan.RepoID != repoID {
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
		if errors.Is(err, db.ErrInvalidBaseline) {
			response.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if isDatabaseUniquenessError(err) {
			response.WriteError(w, http.StatusConflict, "conflict", "baseline already exists")
		} else {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create baseline")
		}
		return
	}

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: baseline})
}
