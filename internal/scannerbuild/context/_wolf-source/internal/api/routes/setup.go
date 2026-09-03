package routes

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
)

const sampleRepoGo = `package main

func main() {
	password := "hunter2-super-secret"
	_ = password
}
`

func GetSetupStatus(w http.ResponseWriter, r *http.Request) {
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
	repos, _ := h.Store.ListReposByUser(r.Context(), claims.UserID)
	cols, _ := h.Store.ListCollectionsByUser(r.Context(), claims.UserID)
	scans, _ := h.Store.ListScansByUser(r.Context(), claims.UserID)
	hasCompleted := false
	for i := range scans {
		if scans[i].Status == models.ScanStatusCompleted {
			hasCompleted = true
			break
		}
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{
		"repo_count":         len(repos),
		"collection_count":   len(cols),
		"has_completed_scan": hasCompleted,
	}})
}

func CreateSampleRepo(w http.ResponseWriter, r *http.Request) {
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
	if existing, err := h.Store.ListReposByUser(r.Context(), claims.UserID); err == nil {
		for i := range existing {
			if strings.EqualFold(existing[i].Name, "sample") {
				response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: existing[i]})
				return
			}
		}
	}
	dir := sampleRepoDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to write sample repo")
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(sampleRepoGo), 0o644); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to write sample repo")
		return
	}
	if rejectCommunityLimit(w, r.Context(), h.Store, limitRepos) {
		return
	}
	now := time.Now().UTC()
	repo := &models.Repo{
		ID:            uuid.NewString(),
		UserID:        claims.UserID,
		Name:          "sample",
		SourceType:    models.SourceTypeLocal,
		SourcePath:    dir,
		DefaultBranch: "main",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := h.Store.CreateRepo(r.Context(), repo); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create sample repo")
		return
	}
	if defID, derr := ensureDefaultCollection(r.Context(), h.Store, claims.UserID); derr == nil {
		_ = h.Store.SetRepoCollection(r.Context(), repo.ID, defID)
	}
	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: repo})
}

func sampleRepoDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".wolf", "sample-repo")
	}
	return filepath.Join(os.TempDir(), "wolf-sample-repo")
}
