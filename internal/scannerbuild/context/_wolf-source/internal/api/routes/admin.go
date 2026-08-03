package routes

import (
	"net/http"

	"github.com/alphabravocompany/thewolf/internal/api/response"
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
