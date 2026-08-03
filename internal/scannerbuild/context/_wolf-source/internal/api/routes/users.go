// User-management handlers are mounted behind the server's administrator
// middleware. Handler-local authentication checks remain defense in depth.
package routes

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// userSummary is the safe-to-serialize subset of models.User. The
// PasswordHash field has json:"-" already, but be explicit here so we
// don't accidentally widen the surface later.
type userSummary struct {
	ID                         string    `json:"id"`
	Email                      string    `json:"email"`
	Role                       string    `json:"role"`
	ScannerSupplyChainPersonas []string  `json:"scanner_supply_chain_personas"`
	ScannerSupplyChainScopes   []string  `json:"scanner_supply_chain_scopes"`
	CreatedAt                  time.Time `json:"created_at"`
	UpdatedAt                  time.Time `json:"updated_at"`
}

func toUserSummary(u models.User) userSummary {
	role := u.Role
	if role == "" {
		role = models.RoleUser
	}
	personas, _ := apikey.DecodeScannerPersonas(u.ScannerSupplyChainPersonas)
	scopes, personas, err := apikey.ScannerScopesForPersonas(personas)
	if err != nil {
		scopes = apikey.ScopeSet{apikey.ScopeReadScannerSupplyChain}
		personas = []string{apikey.ScannerPersonaViewer}
	}
	if role == models.RoleAdmin {
		personas = []string{apikey.ScannerPersonaSupplyChainAdministrator}
		scopes = apikey.ScopeSet{apikey.ScopeAdminScannerSupplyChain}
	}
	return userSummary{
		ID:                         u.ID,
		Email:                      u.Email,
		Role:                       role,
		ScannerSupplyChainPersonas: personas,
		ScannerSupplyChainScopes:   append([]string(nil), scopes...),
		CreatedAt:                  u.CreatedAt,
		UpdatedAt:                  u.UpdatedAt,
	}
}

// validRole normalizes a role string to admin|user, defaulting to user.
func validRole(role string) string {
	if strings.ToLower(strings.TrimSpace(role)) == models.RoleAdmin {
		return models.RoleAdmin
	}
	return models.RoleUser
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
	Email                      string   `json:"email"`
	Password                   string   `json:"password"`
	Role                       string   `json:"role"` // admin | user (default user)
	ScannerSupplyChainPersonas []string `json:"scanner_supply_chain_personas"`
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
	if msg, ok := validatePassword(req.Password); !ok {
		response.WriteError(w, http.StatusBadRequest, "validation_error", msg)
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

	role := validRole(req.Role)
	encodedPersonas := ""
	if role != models.RoleAdmin {
		var normalizeErr error
		encodedPersonas, _, normalizeErr = apikey.EncodeScannerPersonas(req.ScannerSupplyChainPersonas)
		if normalizeErr != nil {
			response.WriteError(w, http.StatusBadRequest, "validation_error", normalizeErr.Error())
			return
		}
	} else if len(req.ScannerSupplyChainPersonas) > 0 {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "system administrators have implicit full scanner access; do not assign scanner personas")
		return
	}

	now := time.Now()
	u := &models.User{
		ID:                         uuid.New().String(),
		Email:                      req.Email,
		PasswordHash:               hash,
		Role:                       role,
		ScannerSupplyChainPersonas: encodedPersonas,
		CreatedAt:                  now,
		UpdatedAt:                  now,
	}
	if err := h.Store.CreateUser(r.Context(), u); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create user")
		return
	}

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: toUserSummary(*u)})
}

type updateUserRoleRequest struct {
	Role string `json:"role"` // admin | user
}

// UpdateUserRole handles PUT /api/users/{id}/role — admin-only promote/demote.
// A user can't demote themselves (so the system can't be left with no admin by
// accident); the route group is already admin-gated.
func UpdateUserRole(w http.ResponseWriter, r *http.Request) {
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
	if id == claims.UserID {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "you cannot change your own role")
		return
	}
	var req updateUserRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	u, err := h.Store.GetUserByID(r.Context(), id)
	if err != nil || u == nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	wasAdmin := u.IsAdmin()
	u.Role = validRole(req.Role)
	if wasAdmin && u.Role == models.RoleUser {
		encodedViewer, _, _ := apikey.EncodeScannerPersonas([]string{apikey.ScannerPersonaViewer})
		u.ScannerSupplyChainPersonas = encodedViewer
	}
	if err := h.Store.UpdateUser(r.Context(), u); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to update user role")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: toUserSummary(*u)})
}

type updateScannerSupplyChainAccessRequest struct {
	Personas *[]string `json:"personas"`
}

// UpdateUserScannerSupplyChainAccess replaces a regular user's composable,
// predefined scanner personas. Arbitrary scopes are never accepted here.
func UpdateUserScannerSupplyChainAccess(w http.ResponseWriter, r *http.Request) {
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
	if id == claims.UserID {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "you cannot change your own scanner supply-chain access")
		return
	}
	u, err := h.Store.GetUserByID(r.Context(), id)
	if err != nil || u == nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if u.IsAdmin() {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "system administrators have implicit full scanner access")
		return
	}
	var req updateScannerSupplyChainAccessRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "body must contain only a personas array")
		return
	}
	if req.Personas == nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "personas is required")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "request body must contain exactly one JSON object")
		return
	}
	encoded, _, err := apikey.EncodeScannerPersonas(*req.Personas)
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if err := h.Store.UpdateUserScannerSupplyChainPersonas(r.Context(), u.ID, encoded); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to update scanner supply-chain access")
		return
	}
	u.ScannerSupplyChainPersonas = encoded
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: toUserSummary(*u)})
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
