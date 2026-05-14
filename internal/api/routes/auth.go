package routes

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
)

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func Register(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	var req registerRequest
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

	// Check if user already exists
	existing, _ := h.Store.GetUserByEmail(r.Context(), req.Email)
	if existing != nil {
		response.WriteError(w, http.StatusConflict, "conflict", "email already registered")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to hash password")
		return
	}

	now := time.Now()
	user := &models.User{
		ID:           uuid.New().String(),
		Email:        req.Email,
		PasswordHash: hash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := h.Store.CreateUser(r.Context(), user); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create user")
		return
	}

	tokens, err := auth.GenerateToken(user.ID, user.Email)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to generate token")
		return
	}

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{
		Data: map[string]interface{}{
			"user":          user,
			"access_token":  tokens.AccessToken,
			"refresh_token": tokens.RefreshToken,
		},
	})
}

func Login(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || req.Password == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "email and password are required")
		return
	}

	user, err := h.Store.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid email or password")
		return
	}

	ok, err := auth.VerifyPassword(req.Password, user.PasswordHash)
	if err != nil || !ok {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid email or password")
		return
	}

	tokens, err := auth.GenerateToken(user.ID, user.Email)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to generate token")
		return
	}

	// Note: we used to also set an HttpOnly wolf_token cookie here, but
	// browsers won't let the SPA's document.cookie write overwrite an
	// existing HttpOnly cookie with the same name. The net effect was
	// that the SPA's setToken() silently failed, getToken() returned
	// null, and the /_authed route guard kicked the user back to /login
	// in an infinite loop. The SPA holds the JS-readable cookie and
	// sends the Bearer header; both auth paths the middleware accepts.

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"user":          user,
			"access_token":  tokens.AccessToken,
			"refresh_token": tokens.RefreshToken,
		},
	})
}

func Logout(w http.ResponseWriter, r *http.Request) {
	// Mirror the Login change: don't write an HttpOnly cookie. If a
	// legacy HttpOnly wolf_token cookie is in flight from a previous
	// build, expire it so we don't leave stale credentials behind.
	// We set Secure+SameSite even on the expiry cookie because some
	// browsers (Chrome 80+ with SameSite=None) require Secure on every
	// Set-Cookie targeting the same name — without it, the expiry write
	// gets dropped and the stale cookie hangs around.
	http.SetCookie(w, &http.Cookie{
		Name:     "wolf_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}) //nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]string{"message": "logged out"}})
}

func Me(w http.ResponseWriter, r *http.Request) {
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

	user, err := h.Store.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: user})
}

func ChangePassword(w http.ResponseWriter, r *http.Request) {
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

	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	if len(req.NewPassword) < 8 {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "new password must be at least 8 characters")
		return
	}

	user, err := h.Store.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}

	ok, err := auth.VerifyPassword(req.CurrentPassword, user.PasswordHash)
	if err != nil || !ok {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "current password is incorrect")
		return
	}

	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to hash password")
		return
	}

	user.PasswordHash = hash
	user.UpdatedAt = time.Now()
	if err := h.Store.UpdateUser(r.Context(), user); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to update password")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]string{"message": "password updated"}})
}
