package routes

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/scantarget"
)

// AdminListTokens lists every user's API tokens for the admin oversight view.
// Tokens are hash-only (the plaintext is shown once at creation and never
// stored), so only metadata is ever returned. The user_id field lets the UI
// attribute each token to its owner. Admin-gated.
//
// Route: GET /api/admin/tokens
func AdminListTokens(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	tokens, err := h.Store.ListAllAPITokens(r.Context())
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list tokens")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: tokens,
		Meta: response.ListMeta{Total: len(tokens), Page: 1, PerPage: len(tokens)},
	})
}

// adminMaskedSecret is a secret in the admin view: masked value + the owner's
// user_id. The plaintext of another user's secret is never exposed.
type adminMaskedSecret struct {
	maskedSecret
	UserID string `json:"user_id"`
}

// AdminListSecrets lists every user's secrets for the admin oversight view,
// MASKED — existence + metadata only, never decrypted plaintext. Admin-gated.
//
// Route: GET /api/admin/secrets
func AdminListSecrets(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	secs, err := h.Store.ListAllSecrets(r.Context())
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list secrets")
		return
	}
	masked := make([]adminMaskedSecret, len(secs))
	for i, s := range secs {
		masked[i] = adminMaskedSecret{
			maskedSecret: maskedSecret{
				ID:        s.ID,
				KeyType:   s.KeyType,
				KeyName:   s.KeyName,
				Value:     maskedStoredSecret(s),
				CreatedAt: s.CreatedAt,
			},
			UserID: s.UserID,
		}
	}
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: masked,
		Meta: response.ListMeta{Total: len(masked), Page: 1, PerPage: len(masked)},
	})
}

func AdminDisk(w http.ResponseWriter, r *http.Request) {
	artifactsRoot := ""
	if artifacts.Global != nil {
		artifactsRoot = artifacts.Global.Root()
	}
	if artifactsRoot == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			artifactsRoot = filepath.Join(home, ".wolf", "artifacts")
		}
	}
	workspacesBytes := int64(0)
	if root := strings.TrimSpace(os.Getenv("WOLF_WORKSPACE_ROOT")); root != "" {
		workspacesBytes = dirBytes(root)
	}
	dbPath := strings.TrimSpace(os.Getenv("WOLF_DB_DSN"))
	if dbPath == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			dbPath = filepath.Join(home, ".wolf", "wolf.db")
		}
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{
		"artifacts_bytes":  dirBytes(artifactsRoot),
		"workspaces_bytes": workspacesBytes,
		"db_bytes":         fileBytes(dbPath),
	}})
}

func AdminReapWorkspaces(w http.ResponseWriter, r *http.Request) {
	maxAgeHours := 72
	var req struct {
		MaxAgeHours int `json:"max_age_hours"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.MaxAgeHours > 0 {
			maxAgeHours = req.MaxAgeHours
		}
	}
	root := strings.TrimSpace(os.Getenv("WOLF_WORKSPACE_ROOT"))
	if root == "" {
		response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{"removed": 0}})
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list workspaces")
		return
	}
	cutoff := time.Now().Add(-time.Duration(maxAgeHours) * time.Hour)
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "wolf-git-scan-") && !strings.HasPrefix(name, "wolf-ssh-scan-") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := scantarget.CleanupWorkspace(filepath.Join(root, name)); err == nil {
			removed++
		}
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{"removed": removed}})
}

func dirBytes(root string) int64 {
	if strings.TrimSpace(root) == "" {
		return 0
	}
	var n int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err == nil {
			n += info.Size()
		}
		return nil
	})
	return n
}

func fileBytes(path string) int64 {
	if strings.TrimSpace(path) == "" || strings.Contains(path, "://") {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return 0
	}
	return info.Size()
}
