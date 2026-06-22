package routes

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

type createTokenRequest struct {
	Name string `json:"name"`
	// Scopes accepts concrete scopes (e.g. "read:scans") or the aliases
	// "read-only" and "full".
	Scopes []string `json:"scopes"`
	// ExpiresInDays: omitted -> 90-day default; 0 -> never expires.
	ExpiresInDays *int `json:"expires_in_days"`
}

// tokenCreateResponse is the create reply — the API token plus the plaintext
// secret, which is shown exactly once and never retrievable again.
type tokenCreateResponse struct {
	*models.APIToken
	Token string `json:"token"`
}

// ListAPITokens returns the caller's API tokens (metadata only).
func ListAPITokens(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	tokens, err := h.Store.ListAPITokensByUser(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list tokens")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: tokens,
		Meta: response.ListMeta{Total: len(tokens), Page: 1, PerPage: len(tokens)},
	})
}

// CreateAPIToken mints a new API token for the caller.
func CreateAPIToken(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	var req createTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_failed", "token name is required")
		return
	}
	scopes, err := apikey.ParseScopes(req.Scopes)
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	info := auth.GetAuthInfo(r.Context())
	if info == nil || !info.Scopes.AllowsDelegation(scopes) {
		response.WriteError(w, http.StatusForbidden, "insufficient_scope", "requested scopes exceed current credential")
		return
	}
	if apikey.ScopeSet(scopes).Has(apikey.ScopeAdmin) && !claims.IsAdmin() {
		response.WriteError(w, http.StatusForbidden, "forbidden", "admin scope requires an administrator")
		return
	}

	days := apikey.DefaultExpiryDays
	if req.ExpiresInDays != nil {
		days = *req.ExpiresInDays
	}
	if days < 0 {
		response.WriteError(w, http.StatusBadRequest, "validation_failed", "expires_in_days cannot be negative")
		return
	}
	var expiresAt *time.Time
	if days > 0 {
		t := time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour)
		expiresAt = &t
	}

	plaintext, hash, prefix, err := apikey.Generate()
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to generate token")
		return
	}
	token := &models.APIToken{
		ID:          uuid.New().String(),
		UserID:      claims.UserID,
		Name:        req.Name,
		TokenHash:   hash,
		TokenPrefix: prefix,
		ScopeList:   scopes,
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   expiresAt,
	}
	if err := h.Store.CreateAPIToken(r.Context(), token); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create token")
		return
	}
	token.ScopeList = scopes
	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{
		Data: tokenCreateResponse{APIToken: token, Token: plaintext},
	})
}

// RevokeAPIToken revokes one of the caller's API tokens. Admins (or admin-
// scoped tokens) may revoke any user's token.
func RevokeAPIToken(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	id := chi.URLParam(r, "id")
	token, err := h.Store.GetAPITokenByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "token not found")
		return
	}
	info := auth.GetAuthInfo(r.Context())
	isAdmin := claims.IsAdmin() && info != nil && info.Scopes.Has(apikey.ScopeAdmin)
	if token.UserID != claims.UserID && !isAdmin {
		response.WriteError(w, http.StatusForbidden, "forbidden", "cannot revoke another user's token")
		return
	}
	if err := h.Store.RevokeAPIToken(r.Context(), id); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to revoke token")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListAuditLog returns recent mutating-request audit entries (admin scope).
func ListAuditLog(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	q := r.URL.Query()
	perPage := 25
	if v := q.Get("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			perPage = n
		}
	} else if v := q.Get("limit"); v != "" { // back-compat with the old ?limit=
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			perPage = n
		}
	}
	page := 1
	if v := q.Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	sortBy := "time"
	if q.Get("sort") == "status" {
		sortBy = "status"
	}
	// Default newest / highest-first unless explicitly ascending.
	desc := q.Get("order") != "asc"

	entries, total, err := h.Store.QueryAuditLog(r.Context(), db.AuditQuery{
		Search:   q.Get("q"),
		Method:   q.Get("method"),
		Category: q.Get("category"),
		Severity: q.Get("severity"),
		SortBy:   sortBy,
		Desc:     desc,
		Limit:    perPage,
		Offset:   (page - 1) * perPage,
	})
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list audit log")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: entries,
		Meta: response.ListMeta{Total: total, Page: page, PerPage: perPage},
	})
}
