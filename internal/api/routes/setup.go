package routes

import (
	"context"
	"embed"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	gitpkg "github.com/alphabravocompany/thewolf/internal/gitops"
	"github.com/alphabravocompany/thewolf/internal/models"
)

//go:embed sample/*
var sampleFiles embed.FS

// ponytail: serialize this infrequent local setup; use a filesystem lock if
// multiple API processes ever share the same sample directory.
var sampleRepoMu sync.Mutex

const legacySampleRepoGo = `package main

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
	sampleRepoMu.Lock()
	defer sampleRepoMu.Unlock()
	dir := sampleRepoDir()
	existing, err := h.Store.ListReposByUser(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to look up sample repo")
		return
	}
	for i := range existing {
		if strings.EqualFold(existing[i].Name, "sample") {
			if existing[i].SourceType == models.SourceTypeLocal && existing[i].SourcePath == dir {
				if err := ensureSampleRepo(r.Context(), dir); err != nil {
					response.WriteError(w, http.StatusInternalServerError, "server_error", err.Error())
					return
				}
			}
			response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: existing[i]})
			return
		}
	}
	if err := ensureSampleRepo(r.Context(), dir); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", err.Error())
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

func ensureSampleRepo(ctx context.Context, dir string) error {
	legacy := false
	if info, err := os.Lstat(dir); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("sample repository path must be a directory, not a file or symlink")
		}
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return nil // Never reset or overwrite an existing user's repository.
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect sample repository: %w", err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("inspect legacy sample: %w", err)
		}
		if len(entries) != 1 || entries[0].Name() != "main.go" || !entries[0].Type().IsRegular() {
			return fmt.Errorf("sample directory contains user files; move it aside before adding the sample again")
		}
		content, err := os.ReadFile(filepath.Join(dir, "main.go"))
		if err != nil || string(content) != legacySampleRepoGo {
			return fmt.Errorf("sample main.go has been modified; move the directory aside before adding the sample again")
		}
		legacy = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect sample directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return fmt.Errorf("create sample parent: %w", err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(dir), ".sample-repo-")
	if err != nil {
		return fmt.Errorf("create sample staging directory: %w", err)
	}
	defer os.RemoveAll(stage)
	entries, err := sampleFiles.ReadDir("sample")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		content, err := sampleFiles.ReadFile("sample/" + entry.Name())
		if err != nil {
			return err
		}
		// .txt keeps the embedded Go module outside the host module's build.
		if err := os.WriteFile(filepath.Join(stage, strings.TrimSuffix(entry.Name(), ".txt")), content, 0o644); err != nil {
			return fmt.Errorf("write sample file: %w", err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for _, args := range [][]string{
		{"init", "--initial-branch=main", "--object-format=sha1", "--template="},
		{"add", "--", "."},
		{"commit", "-m", "Intentionally vulnerable Wolf sample"},
	} {
		cmd := gitpkg.Command(ctx, []string{
			"GIT_AUTHOR_NAME=Wolf Sample", "GIT_AUTHOR_EMAIL=sample@example.invalid",
			"GIT_COMMITTER_NAME=Wolf Sample", "GIT_COMMITTER_EMAIL=sample@example.invalid",
			"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
		}, args...)
		cmd.Dir = stage
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("create sample Git repository (git must be installed): %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	if legacy {
		// Keep the original directory intact, even after a successful upgrade.
		backup := stage + "-legacy"
		if err := os.Rename(dir, backup); err != nil {
			return fmt.Errorf("preserve legacy sample: %w", err)
		}
		if err := os.Rename(stage, dir); err != nil {
			if restoreErr := os.Rename(backup, dir); restoreErr != nil {
				return fmt.Errorf("install sample: %v; original remains at %s: %w", err, backup, restoreErr)
			}
			return fmt.Errorf("install sample: %w", err)
		}
		return nil
	}
	return os.Rename(stage, dir)
}

func sampleRepoDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".wolf", "sample-repo")
	}
	return filepath.Join(os.TempDir(), "wolf-sample-repo")
}
