package routes

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	gitpkg "github.com/alphabravocompany/thewolf/internal/fix/git"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/detector"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

type createRepoRequest struct {
	Name          string            `json:"name"`
	SourceType    models.SourceType `json:"source_type"`
	SourcePath    string            `json:"source_path"`
	DefaultBranch string            `json:"default_branch"`
}

// createRepoResult is the CreateRepo response body. It embeds the repo so
// existing clients still read `data.id`, `data.name`, etc. unchanged, and
// adds `deduplicated` — true when an add request matched an existing repo
// and we returned that one instead of creating a duplicate row.
type createRepoResult struct {
	*models.Repo
	Deduplicated bool `json:"deduplicated,omitempty"`
}

// normalizeSourcePath trims a single trailing slash so "/repos/x" and
// "/repos/x/" are treated as the same repo for dedup purposes.
func normalizeSourcePath(p string) string {
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		return p[:len(p)-1]
	}
	return p
}

type updateRepoRequest struct {
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`
}

func ListRepos(w http.ResponseWriter, r *http.Request) {
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

	repos, err := h.Store.ListReposByUser(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list repos")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: repos,
		Meta: response.ListMeta{Total: len(repos), Page: 1, PerPage: len(repos)},
	})
}

func CreateRepo(w http.ResponseWriter, r *http.Request) {
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

	var req createRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	if req.Name == "" || req.SourcePath == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "name and source_path are required")
		return
	}
	if req.SourceType == "" {
		req.SourceType = models.SourceTypeLocal
	}
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
	}

	// Dedup: if the user already has a repo at this path, return that one
	// instead of creating a duplicate. Match on normalized source_path
	// (trailing-slash insensitive), scoped to the requesting user.
	if existing, err := h.Store.ListReposByUser(r.Context(), claims.UserID); err == nil {
		want := normalizeSourcePath(req.SourcePath)
		for i := range existing {
			if normalizeSourcePath(existing[i].SourcePath) == want {
				response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
					Data: createRepoResult{Repo: &existing[i], Deduplicated: true},
				})
				return
			}
		}
	}

	now := time.Now()
	repo := &models.Repo{
		ID:            uuid.New().String(),
		UserID:        claims.UserID,
		Name:          req.Name,
		SourceType:    req.SourceType,
		SourcePath:    req.SourcePath,
		DefaultBranch: req.DefaultBranch,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := h.Store.CreateRepo(r.Context(), repo); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create repo")
		return
	}

	// Run detection asynchronously so the response isn't blocked.
	go runDetection(h.Store, repo.ID, repo.SourcePath)

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{
		Data: createRepoResult{Repo: repo},
	})
}

func GetRepo(w http.ResponseWriter, r *http.Request) {
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
	repo, err := h.Store.GetRepoByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}


	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: repo})
}

func UpdateRepo(w http.ResponseWriter, r *http.Request) {
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
	repo, err := h.Store.GetRepoByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}


	var req updateRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	if req.Name != "" {
		repo.Name = req.Name
	}
	if req.DefaultBranch != "" {
		repo.DefaultBranch = req.DefaultBranch
	}
	repo.UpdatedAt = time.Now()

	if err := h.Store.UpdateRepo(r.Context(), repo); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to update repo")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: repo})
}

func DeleteRepo(w http.ResponseWriter, r *http.Request) {
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
	repo, err := h.Store.GetRepoByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}

	scanIDs, err := h.Store.DeleteRepoCascade(r.Context(), id)
	if err != nil {
		wolflog.Error().Err(err).Str("repo_id", id).Msg("delete repo cascade failed")
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to delete repo: "+err.Error())
		return
	}

	// Clean up artifact files on disk.
	if len(scanIDs) > 0 {
		go artifacts.Global.DeleteScans(scanIDs)
	}

	wolflog.Info().Str("repo_id", id).Str("repo_name", repo.Name).Int("scans_deleted", len(scanIDs)).Msg("repo deleted with cascade")

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]interface{}{
		"message":       "repo deleted",
		"scans_deleted": len(scanIDs),
	}})
}

// ListRepoBranches handles GET /api/repos/{id}/branches — returns available git branches.
func ListRepoBranches(w http.ResponseWriter, r *http.Request) {
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
	repo, err := h.Store.GetRepoByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}

	branches, err := gitpkg.ListBranches(repo.SourcePath)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list branches: "+err.Error())
		return
	}
	sort.Strings(branches)

	// Determine current branch
	current, _ := gitpkg.CurrentBranch(repo.SourcePath)

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"branches":       branches,
			"default_branch": repo.DefaultBranch,
			"current_branch": current,
		},
	})
}

// runDetection runs language/framework detection on a repo and caches the results.
// It is designed to be called in a goroutine so it doesn't block the HTTP response.
func runDetection(store db.Store, repoID, sourcePath string) {
	result, err := detector.Detect(sourcePath)
	if err != nil {
		log.Printf("detection failed for repo %s: %v", repoID, err)
		return
	}

	// Serialize languages map to JSON.
	langs := make(map[string]int, len(result.Languages))
	for lang, count := range result.Languages {
		langs[string(lang)] = count
	}
	langsJSON, _ := json.Marshal(langs)
	fwJSON, _ := json.Marshal(result.Frameworks)

	if err := store.UpdateRepoDetection(context.Background(), repoID, string(langsJSON), string(fwJSON)); err != nil {
		log.Printf("failed to save detection for repo %s: %v", repoID, err)
	}
}
