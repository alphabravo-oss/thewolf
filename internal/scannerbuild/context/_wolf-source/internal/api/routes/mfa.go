package routes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

// mfaRequiredSetting is the administrator's organization-wide MFA policy.
const mfaRequiredSetting = "mfa_required"

func parseMFARequired(value string) (bool, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false, nil
	}
	return strconv.ParseBool(value)
}

// mfaRequiredEnabled distinguishes an absent policy (optional MFA) from a
// policy that could not be read or parsed. Unknown policy must not grant access.
func mfaRequiredEnabled(ctx context.Context) (bool, error) {
	h := DefaultHandler
	if h == nil || h.Store == nil {
		return false, http.ErrServerClosed
	}
	value, err := h.Store.GetSetting(ctx, mfaRequiredSetting)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return parseMFARequired(value)
}

func writeMFAPolicyUnavailable(w http.ResponseWriter) {
	response.WriteError(w, http.StatusServiceUnavailable, "mfa_policy_unavailable", "MFA policy is unavailable; retry shortly")
}

type mfaCodeRequest struct {
	Code string `json:"code"`
}

// MFAStatus reports the caller's MFA state and whether it's organizationally
// required. GET /api/auth/mfa/status
func MFAStatus(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	claims := auth.GetUserFromContext(r.Context())
	if h == nil || claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	user, err := h.Store.GetUserByID(r.Context(), claims.UserID)
	if err != nil || user == nil {
		response.WriteError(w, http.StatusServiceUnavailable, "mfa_state_unavailable", "MFA state is unavailable; retry shortly")
		return
	}
	required, err := mfaRequiredEnabled(r.Context())
	if err != nil {
		writeMFAPolicyUnavailable(w)
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"enabled":  user.MFAEnabled(),
			"required": required,
		},
	})
}

// MFASetup begins enrollment: it generates a fresh secret (stored pending,
// encrypted, not yet active) and returns the QR + otpauth URI for the user's
// authenticator app. POST /api/auth/mfa/setup
func MFASetup(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	claims := auth.GetUserFromContext(r.Context())
	if h == nil || claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	user, err := h.Store.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if user.MFAEnabled() {
		response.WriteError(w, http.StatusConflict, "mfa_already_enabled", "disable MFA before re-enrolling")
		return
	}

	key, err := auth.GenerateTOTPSecret(user.Email)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to generate secret")
		return
	}
	qr, err := auth.TOTPQRDataURI(key)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to render qr")
		return
	}
	enc, err := secrets.Encrypt(key.Secret())
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to store secret")
		return
	}

	previous := *user
	// Persist as pending: secret set, not yet enabled, no recovery codes.
	user.TOTPSecret = enc
	user.TOTPEnabled = false
	user.TOTPRecoveryCodes = ""
	user.TOTPLastStep = -1
	if !saveUserMFA(w, r, h, user, &previous) {
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"otpauth_uri": key.String(),
			"secret":      key.Secret(), // for manual entry if the QR won't scan
			"qr":          qr,
		},
	})
}

// MFAActivate confirms enrollment: the user submits a code from their app; on
// success MFA is switched on and one-time recovery codes are returned (once).
// POST /api/auth/mfa/activate
func MFAActivate(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	claims := auth.GetUserFromContext(r.Context())
	if h == nil || claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	var req mfaCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	user, err := h.Store.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if user.MFAEnabled() {
		response.WriteError(w, http.StatusConflict, "mfa_already_enabled", "MFA is already enabled")
		return
	}
	if user.TOTPSecret == "" {
		response.WriteError(w, http.StatusBadRequest, "mfa_not_started", "start enrollment first")
		return
	}
	secret, err := secrets.Decrypt(user.TOTPSecret)
	step, valid := auth.ValidateTOTP(req.Code, secret, time.Now())
	if err != nil || !valid || step <= user.TOTPLastStep {
		response.WriteError(w, http.StatusBadRequest, "invalid_code", "that code is invalid or already used; wait for a new code")
		return
	}

	plain, hashed, err := auth.GenerateRecoveryCodes()
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to generate recovery codes")
		return
	}
	codesJSON, _ := json.Marshal(hashed)
	previous := *user
	user.TOTPEnabled = true
	user.TOTPLastStep = step
	user.TOTPRecoveryCodes = string(codesJSON)
	if !saveUserMFA(w, r, h, user, &previous) {
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"enabled":        true,
			"recovery_codes": plain, // shown exactly once
		},
	})
}

// MFADisable turns MFA off after verifying a current code (or recovery code).
// Blocked while the org mandates MFA (an admin reset is the only path then).
// POST /api/auth/mfa/disable
func MFADisable(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	claims := auth.GetUserFromContext(r.Context())
	if h == nil || claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	required, err := mfaRequiredEnabled(r.Context())
	if err != nil {
		writeMFAPolicyUnavailable(w)
		return
	}
	if required {
		response.WriteError(w, http.StatusForbidden, "mfa_required", "your administrator requires MFA; it cannot be disabled")
		return
	}
	var req mfaCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	user, err := h.Store.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if !user.MFAEnabled() {
		response.WriteError(w, http.StatusBadRequest, "mfa_not_enabled", "MFA is not enabled")
		return
	}
	if _, ok := verifyMFACode(user, req.Code); !ok {
		response.WriteError(w, http.StatusBadRequest, "invalid_code", "that code is invalid or already used; wait for a new code or use an unused recovery code")
		return
	}

	previous := *user
	clearUserMFA(user)
	if !saveUserMFA(w, r, h, user, &previous) {
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]bool{"enabled": false}})
}

// MFALogin completes a two-step login: it exchanges a challenge token plus a
// valid TOTP (or recovery) code for a real session. Public (no session yet).
// POST /api/auth/mfa/login
func MFALogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	var req struct {
		MFAToken string `json:"mfa_token"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	challenge, err := auth.ValidateMFAChallenge(req.MFAToken)
	if err != nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "mfa session expired, log in again")
		return
	}
	user, err := h.Store.GetUserByID(r.Context(), challenge.UserID)
	if err != nil || !user.MFAEnabled() || *challenge.CredentialVersion != user.CredentialVersion {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid mfa state")
		return
	}

	next, ok := verifyMFACode(user, req.Code)
	if !ok {
		RecordAuthEvent(r, user.ID, "auth.login.failed", "warning", http.StatusUnauthorized)
		response.WriteError(w, http.StatusUnauthorized, "invalid_code", "that code is invalid or already used; wait for a new code or use an unused recovery code")
		return
	}
	previous := *user
	user = next
	tokens, err := auth.GenerateToken(user.ID, user.Email)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to generate token")
		return
	}
	refresh, err := refreshSession(tokens)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create session")
		return
	}
	plaintext, browser, err := newBrowserSession(user.ID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create session")
		return
	}
	if err := h.Store.CompleteMFALogin(r.Context(), auth.HashSessionToken(req.MFAToken), user, &previous, refresh, browser); err != nil {
		writeMFAError(w, err)
		return
	}
	setSessionCookie(w, r, plaintext)
	RecordAuthEvent(r, user.ID, "auth.login", "info", http.StatusOK)
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"user":          user,
			"access_token":  tokens.AccessToken,
			"refresh_token": tokens.RefreshToken,
		},
	})
}

// AdminResetUserMFA clears a user's second factor (e.g. lost device). Admin
// only. POST /api/users/{id}/mfa/reset
func AdminResetUserMFA(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	id := chi.URLParam(r, "id")
	user, err := h.Store.GetUserByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	previous := *user
	clearUserMFA(user)
	if !saveUserMFA(w, r, h, user, &previous) {
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]bool{"reset": true}})
}

// MFAEnrollmentGuard confines unenrolled human sessions when MFA is required.
// Policy failure denies business operations; enrollment, identity and logout
// remain reachable, and scoped machine credentials retain their exemption.
func MFAEnrollmentGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := auth.GetUserFromContext(r.Context())
		if claims == nil {
			response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
			return
		}
		if auth.TokenIDFromContext(r.Context()) != "" {
			next.ServeHTTP(w, r)
			return
		}
		// Match actual endpoint/method pairs, never an arbitrary path suffix.
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/auth/mfa/status", "POST /api/v1/auth/mfa/setup", "POST /api/v1/auth/mfa/activate",
			"POST /api/v1/auth/logout", "GET /api/v1/auth/me":
			next.ServeHTTP(w, r)
			return
		}
		required, err := mfaRequiredEnabled(r.Context())
		if err != nil {
			writeMFAPolicyUnavailable(w)
			return
		}
		if !required {
			next.ServeHTTP(w, r)
			return
		}
		user, err := DefaultHandler.Store.GetUserByID(r.Context(), claims.UserID)
		if err != nil || user == nil {
			response.WriteError(w, http.StatusServiceUnavailable, "mfa_state_unavailable", "MFA state is unavailable; retry shortly")
			return
		}
		if user.MFAEnabled() {
			next.ServeHTTP(w, r)
			return
		}
		response.WriteError(w, http.StatusForbidden, "mfa_enrollment_required",
			"your administrator requires two-factor authentication; enroll to continue")
	})
}

// verifyMFACode returns the candidate factor state with a TOTP step or
// recovery code consumed. The caller must conditionally persist it against user.
func verifyMFACode(user *models.User, code string) (*models.User, bool) {
	next := *user
	if secret, err := secrets.Decrypt(user.TOTPSecret); err == nil {
		if step, ok := auth.ValidateTOTP(code, secret, time.Now()); ok && step > user.TOTPLastStep {
			next.TOTPLastStep = step
			return &next, true
		}
	}
	var hashes []string
	if user.TOTPRecoveryCodes != "" {
		_ = json.Unmarshal([]byte(user.TOTPRecoveryCodes), &hashes)
	}
	if idx, matched := auth.MatchRecoveryCode(code, hashes); matched {
		hashes = append(hashes[:idx], hashes[idx+1:]...)
		b, _ := json.Marshal(hashes)
		next.TOTPRecoveryCodes = string(b)
		return &next, true
	}
	return nil, false
}

// clearUserMFA zeroes all second-factor state on the user struct (not yet
// persisted).
func clearUserMFA(user *models.User) {
	user.TOTPSecret = ""
	user.TOTPEnabled = false
	user.TOTPRecoveryCodes = ""
	user.TOTPLastStep = -1
}

// saveUserMFA uses the state actually verified by the caller. A lost race
// requires a fresh read/verification; never overwrite the winner's credentials.
func saveUserMFA(w http.ResponseWriter, r *http.Request, h *Handler, user, previous *models.User) bool {
	if err := h.Store.UpdateUserMFA(r.Context(), user, previous); err != nil {
		writeMFAError(w, err)
		return false
	}
	return true
}

func writeMFAError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrMFAChanged):
		response.WriteError(w, http.StatusConflict, "mfa_state_changed", "MFA state changed; retry with the current enrollment")
	case errors.Is(err, db.ErrMFAChallenge):
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "mfa session expired or already used, log in again")
	default:
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to update MFA state")
	}
}
