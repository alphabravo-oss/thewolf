package routes

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/github"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

type listOrgReposRequest struct {
	Org      string `json:"org"`
	SecretID string `json:"secret_id"` // optional; fall back to user's first github_token
}

// ListOrgGitHubRepos handles POST /sources/github/list-org-repos.
//
// It resolves a github_token secret (either by explicit secret_id or the first
// one owned by the caller), decrypts it, and asks GitHub for every repo in the
// given org (falling back to a user account if the org 404s).
func ListOrgGitHubRepos(w http.ResponseWriter, r *http.Request) {
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

	var req listOrgReposRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Org = strings.TrimSpace(req.Org)
	if req.Org == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "org is required")
		return
	}

	secs, err := h.Store.ListSecretsByUser(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list secrets")
		return
	}

	var chosen *models.Secret
	if req.SecretID != "" {
		for i := range secs {
			if secs[i].ID == req.SecretID && secs[i].KeyType == models.KeyTypeGitHubToken {
				chosen = &secs[i]
				break
			}
		}
	} else {
		for i := range secs {
			if secs[i].KeyType == models.KeyTypeGitHubToken {
				chosen = &secs[i]
				break
			}
		}
	}
	if chosen == nil {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "no github_token secret available")
		return
	}

	token, err := secrets.Decrypt(chosen.EncryptedValue)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to decrypt secret")
		return
	}

	repos, err := github.New(token).ListOrgRepos(r.Context(), req.Org)
	if err != nil {
		response.WriteError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}

	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: repos,
		Meta: response.ListMeta{Total: len(repos), Page: 1, PerPage: len(repos)},
	})
}
