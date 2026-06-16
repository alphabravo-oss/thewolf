package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

// mfaRequiredSetting is the admin toggle that forces every user to enroll a
// second factor. Default off (absent/anything-but-"true").
const mfaRequiredSetting = "mfa_required"

// mfaRequiredEnabled reports whether the org mandates MFA for all users.
func mfaRequiredEnabled(ctx context.Context) bool {
	h := DefaultHandler
	if h == nil {
		return false
	}
	value, err := h.Store.GetSetting(ctx, mfaRequiredSetting)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(value), "true")
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
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"enabled":  user.MFAEnabled(),
			"required": mfaRequiredEnabled(r.Context()),
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

	// Persist as pending: secret set, not yet enabled, no recovery codes.
	user.TOTPSecret = enc
	user.TOTPEnabled = false
	user.TOTPRecoveryCodes = ""
	if err := h.Store.UpdateUserMFA(r.Context(), user); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to save enrollment")
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
	if user.TOTPSecret == "" {
		response.WriteError(w, http.StatusBadRequest, "mfa_not_started", "start enrollment first")
		return
	}
	secret, err := secrets.Decrypt(user.TOTPSecret)
	if err != nil || !auth.ValidateTOTP(req.Code, secret) {
		response.WriteError(w, http.StatusBadRequest, "invalid_code", "that code is not valid")
		return
	}

	plain, hashed, err := auth.GenerateRecoveryCodes()
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to generate recovery codes")
		return
	}
	codesJSON, _ := json.Marshal(hashed)
	user.TOTPEnabled = true
	user.TOTPRecoveryCodes = string(codesJSON)
	if err := h.Store.UpdateUserMFA(r.Context(), user); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to enable mfa")
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
	if mfaRequiredEnabled(r.Context()) {
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
	if ok, _, _ := verifyMFACode(user, req.Code); !ok {
		response.WriteError(w, http.StatusBadRequest, "invalid_code", "that code is not valid")
		return
	}

	clearUserMFA(user)
	if err := h.Store.UpdateUserMFA(r.Context(), user); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to disable mfa")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]bool{"enabled": false}})
}

// MFALogin completes a two-step login: it exchanges a challenge token plus a
// valid TOTP (or recovery) code for a real session. Public (no session yet).
// POST /api/auth/mfa/login
func MFALogin(w http.ResponseWriter, r *http.Request) {
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
	if err != nil || !user.MFAEnabled() {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid mfa state")
		return
	}

	ok, consumedRecovery, newRecoveryJSON := verifyMFACode(user, req.Code)
	if !ok {
		response.WriteError(w, http.StatusUnauthorized, "invalid_code", "that code is not valid")
		return
	}
	// Burn the recovery code if one was used.
	if consumedRecovery {
		user.TOTPRecoveryCodes = newRecoveryJSON
		if err := h.Store.UpdateUserMFA(r.Context(), user); err != nil {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to consume recovery code")
			return
		}
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
	clearUserMFA(user)
	if err := h.Store.UpdateUserMFA(r.Context(), user); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to reset mfa")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]bool{"reset": true}})
}

// mfaEnrollmentAllowlist is the set of path suffixes a not-yet-enrolled user
// may still reach while the org mandates MFA, so they can complete enrollment
// (or sign out). Everything else is blocked until they enroll.
var mfaEnrollmentAllowlist = []string{
	"/auth/mfa/status", "/auth/mfa/setup", "/auth/mfa/activate",
	"/auth/logout", "/auth/me",
}

// MFAEnrollmentGuard confines a session to the enrollment endpoints when the
// org requires MFA but the caller hasn't enrolled. It is a no-op when MFA is
// not mandatory or the caller already has a second factor. Mount it on the
// authenticated group, after auth.Middleware (it needs resolved claims).
func MFAEnrollmentGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if DefaultHandler == nil || !mfaRequiredEnabled(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}
		for _, suffix := range mfaEnrollmentAllowlist {
			if strings.HasSuffix(r.URL.Path, suffix) {
				next.ServeHTTP(w, r)
				return
			}
		}
		claims := auth.GetUserFromContext(r.Context())
		if claims == nil { // unauthenticated requests are handled upstream
			next.ServeHTTP(w, r)
			return
		}
		if user, err := DefaultHandler.Store.GetUserByID(r.Context(), claims.UserID); err == nil && user.MFAEnabled() {
			next.ServeHTTP(w, r)
			return
		}
		response.WriteError(w, http.StatusForbidden, "mfa_enrollment_required",
			"your administrator requires two-factor authentication; enroll to continue")
	})
}

// verifyMFACode checks code against the user's TOTP secret, then their recovery
// codes. On a recovery-code hit it returns the codes JSON with that code
// removed so the caller can persist the consumption.
func verifyMFACode(user *models.User, code string) (ok bool, consumedRecovery bool, newRecoveryJSON string) {
	if secret, err := secrets.Decrypt(user.TOTPSecret); err == nil && auth.ValidateTOTP(code, secret) {
		return true, false, ""
	}
	var hashes []string
	if user.TOTPRecoveryCodes != "" {
		_ = json.Unmarshal([]byte(user.TOTPRecoveryCodes), &hashes)
	}
	if idx, matched := auth.MatchRecoveryCode(code, hashes); matched {
		hashes = append(hashes[:idx], hashes[idx+1:]...)
		b, _ := json.Marshal(hashes)
		return true, true, string(b)
	}
	return false, false, ""
}

// clearUserMFA zeroes all second-factor state on the user struct (not yet
// persisted).
func clearUserMFA(user *models.User) {
	user.TOTPSecret = ""
	user.TOTPEnabled = false
	user.TOTPRecoveryCodes = ""
}
