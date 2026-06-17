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

const minPasswordLength = 12

// maxPasswordLength caps password length. Argon2id hashes the full input, so an
// unbounded password lets an attacker burn server memory/CPU per login attempt
// (a cheap DoS). 128 is comfortably above any human passphrase.
const maxPasswordLength = 128
const sessionDuration = 7 * 24 * time.Hour
const registrationEnabledSetting = "registration_enabled"

// validatePassword enforces the length policy shared by register, change-
// password, and admin user-creation. Returns a user-facing message on failure.
func validatePassword(pw string) (string, bool) {
	if len(pw) < minPasswordLength {
		return "password must be at least 12 characters", false
	}
	if len(pw) > maxPasswordLength {
		return "password must be at most 128 characters", false
	}
	return "", true
}

func Register(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	if !registrationAllowed(r) {
		response.WriteError(w, http.StatusForbidden, "registration_disabled", "self-service registration is disabled")
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
	if msg, ok := validatePassword(req.Password); !ok {
		response.WriteError(w, http.StatusBadRequest, "validation_error", msg)
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

	// The very first account bootstraps the system as an admin; everyone after
	// is a regular user (an admin can promote them from Settings → Users).
	role := models.RoleUser
	if existing, lerr := h.Store.ListUsers(r.Context()); lerr == nil && len(existing) == 0 {
		role = models.RoleAdmin
	}

	now := time.Now()
	user := &models.User{
		ID:           uuid.New().String(),
		Email:        req.Email,
		PasswordHash: hash,
		Role:         role,
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
	if err := issueSessionCookie(w, r, user.ID); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create session")
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

func AuthSettings(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	users, err := h.Store.ListUsers(r.Context())
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to inspect users")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{
		"registration_enabled": registrationSettingEnabled(r),
		"has_users":            len(users) > 0,
	}})
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

	// Second factor: if the user has TOTP active, the password alone earns no
	// session. Hand back a short-lived challenge to be exchanged for a session
	// via POST /auth/mfa/login with a valid code.
	if user.MFAEnabled() {
		challenge, err := auth.GenerateMFAChallenge(user.ID, user.Email)
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to start mfa challenge")
			return
		}
		response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
			Data: map[string]interface{}{
				"mfa_required": true,
				"mfa_token":    challenge,
			},
		})
		return
	}

	tokens, err := auth.GenerateToken(user.ID, user.Email)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to generate token")
		return
	}
	if err := issueSessionCookie(w, r, user.ID); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create session")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"user":          user,
			"access_token":  tokens.AccessToken,
			"refresh_token": tokens.RefreshToken,
			// When the org mandates MFA and this user hasn't enrolled, the
			// session is issued (password was valid) but the enrollment guard
			// confines it to the MFA-setup endpoints until they enroll.
			"enrollment_required": mfaRequiredEnabled(r.Context()) && !user.MFAEnabled(),
		},
	})
}

func Logout(w http.ResponseWriter, r *http.Request) {
	if h := DefaultHandler; h != nil {
		if cookie, err := r.Cookie("wolf_token"); err == nil && auth.LooksLikeSessionToken(cookie.Value) {
			_ = h.Store.RevokeAuthSessionByHash(r.Context(), auth.HashSessionToken(cookie.Value))
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "wolf_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   cookieSecure(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]string{"message": "logged out"}})
}

func issueSessionCookie(w http.ResponseWriter, r *http.Request, userID string) error {
	h := DefaultHandler
	if h == nil {
		return http.ErrServerClosed
	}
	plaintext, hash, prefix, err := auth.GenerateSessionToken()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := h.Store.CreateAuthSession(r.Context(), &models.AuthSession{
		ID:            uuid.New().String(),
		UserID:        userID,
		SessionHash:   hash,
		SessionPrefix: prefix,
		CreatedAt:     now,
		ExpiresAt:     now.Add(sessionDuration),
	}); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "wolf_token",
		Value:    plaintext,
		Path:     "/",
		HttpOnly: true,
		Secure:   cookieSecure(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionDuration.Seconds()),
	})
	return nil
}

func cookieSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func registrationAllowed(r *http.Request) bool {
	h := DefaultHandler
	if h == nil {
		return false
	}
	users, err := h.Store.ListUsers(r.Context())
	if err != nil {
		return false
	}
	if len(users) == 0 {
		return true
	}
	return registrationSettingEnabled(r)
}

func registrationSettingEnabled(r *http.Request) bool {
	h := DefaultHandler
	if h == nil {
		return false
	}
	value, err := h.Store.GetSetting(r.Context(), registrationEnabledSetting)
	if err != nil {
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(value), "false")
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

	if msg, ok := validatePassword(req.NewPassword); !ok {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "new "+msg)
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

// updateProfileRequest carries self-service profile edits. Fields are pointers
// so "not provided" is distinguishable from "set to empty". Changing the email
// (the login identifier) requires re-entering the current password.
type updateProfileRequest struct {
	DisplayName     *string `json:"display_name"`
	Email           *string `json:"email"`
	CurrentPassword string  `json:"current_password"`
}

// UpdateProfile lets the caller change their own display name and email.
// PUT /api/auth/profile
func UpdateProfile(w http.ResponseWriter, r *http.Request) {
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
	var req updateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	user, err := h.Store.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}

	if req.DisplayName != nil {
		user.DisplayName = strings.TrimSpace(*req.DisplayName)
	}

	if req.Email != nil {
		newEmail := strings.TrimSpace(strings.ToLower(*req.Email))
		if newEmail != user.Email {
			if newEmail == "" || !strings.Contains(newEmail, "@") {
				response.WriteError(w, http.StatusBadRequest, "validation_error", "valid email is required")
				return
			}
			// Email is the login identifier — require the current password.
			ok, err := auth.VerifyPassword(req.CurrentPassword, user.PasswordHash)
			if err != nil || !ok {
				response.WriteError(w, http.StatusUnauthorized, "unauthorized", "current password is required to change email")
				return
			}
			if existing, _ := h.Store.GetUserByEmail(r.Context(), newEmail); existing != nil && existing.ID != user.ID {
				response.WriteError(w, http.StatusConflict, "conflict", "that email is already in use")
				return
			}
			user.Email = newEmail
		}
	}

	if err := h.Store.UpdateUserProfile(r.Context(), user); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to update profile")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: user})
}
