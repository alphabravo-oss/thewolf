// User-management endpoints. Any authenticated user can list / create /
// delete users. There's no role model yet — for a self-hosted single-org
// tool this is the simplest workable surface; if we add tenancy or RBAC
// later, this is the right entry point to gate.
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
	"github.com/alphabravocompany/thewolf/internal/models"
)

// userSummary is the safe-to-serialize subset of models.User. The
// PasswordHash field has json:"-" already, but be explicit here so we
// don't accidentally widen the surface later.
type userSummary struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toUserSummary(u models.User) userSummary {
	return userSummary{
		ID:        u.ID,
		Email:     u.Email,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

// ListUsers handles GET /api/users — list every user in the system.
func ListUsers(w http.ResponseWriter, r *http.Request) {
	if auth.GetUserFromContext(r.Context()) == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	users, err := h.Store.ListUsers(r.Context())
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list users")
		return
	}

	out := make([]userSummary, 0, len(users))
	for _, u := range users {
		out = append(out, toUserSummary(u))
	}
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: out,
		Meta: response.ListMeta{Total: len(out), Page: 1, PerPage: len(out)},
	})
}

type adminCreateUserRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// CreateUserAdmin handles POST /api/users — admin-flavor user creation
// (no auto-login, no token returned). The existing /api/auth/register
// covers the self-signup flow; this exists so an authenticated user can
// add another account from the Settings page.
func CreateUserAdmin(w http.ResponseWriter, r *http.Request) {
	if auth.GetUserFromContext(r.Context()) == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	var req adminCreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "valid email is required")
		return
	}
	if len(req.Password) < 8 {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "password must be at least 8 characters")
		return
	}

	if existing, _ := h.Store.GetUserByEmail(r.Context(), req.Email); existing != nil {
		response.WriteError(w, http.StatusConflict, "conflict", "email already registered")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to hash password")
		return
	}

	now := time.Now()
	u := &models.User{
		ID:           uuid.New().String(),
		Email:        req.Email,
		PasswordHash: hash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := h.Store.CreateUser(r.Context(), u); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create user")
		return
	}

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: toUserSummary(*u)})
}

// DeleteUser handles DELETE /api/users/{id} — remove a user. The caller
// is explicitly prevented from deleting themselves; doing so locks the
// remaining sessions out of an account they thought they still had,
// which is almost never what they meant. To delete your own account,
// implement an explicit self-delete endpoint with extra confirmation.
func DeleteUser(w http.ResponseWriter, r *http.Request) {
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
	if id == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "user id is required")
		return
	}
	if id == claims.UserID {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "cannot delete yourself; ask another admin or use account-settings")
		return
	}

	if _, err := h.Store.GetUserByID(r.Context(), id); err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("user %s not found", id))
		return
	}

	// Refuse to delete the last remaining user — leaves the system
	// without any way to log back in.
	users, err := h.Store.ListUsers(r.Context())
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to verify last-user invariant")
		return
	}
	if len(users) <= 1 {
		response.WriteError(w, http.StatusConflict, "conflict", "cannot delete the last user")
		return
	}

	if err := h.Store.DeleteUser(r.Context(), id); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to delete user")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]string{"id": id, "status": "deleted"}})
}
