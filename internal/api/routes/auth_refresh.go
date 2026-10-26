package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func refreshSession(pair *auth.TokenPair) (*models.AuthSession, error) {
	claims, err := auth.ValidateRefreshToken(pair.RefreshToken)
	if err != nil {
		return nil, err
	}
	return &models.AuthSession{ID: claims.ID, UserID: claims.UserID,
		SessionHash: auth.HashSessionToken(pair.RefreshToken), SessionPrefix: "refresh",
		CreatedAt: time.Now().UTC(), ExpiresAt: claims.ExpiresAt.Time}, nil
}

func issueAPITokens(ctx context.Context, userID, email string) (*auth.TokenPair, error) {
	pair, err := auth.GenerateToken(userID, email)
	if err != nil {
		return nil, err
	}
	session, err := refreshSession(pair)
	if err != nil {
		return nil, err
	}
	if err := DefaultHandler.Store.CreateAuthSession(ctx, session); err != nil {
		return nil, err
	}
	return pair, nil
}

func RefreshSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		Token string `json:"refresh_token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil || req.Token == "" {
		response.WriteError(w, http.StatusBadRequest, "invalid_request", "a refresh token is required")
		return
	}
	claims, err := auth.ValidateRefreshToken(req.Token)
	if err != nil {
		response.WriteError(w, http.StatusUnauthorized, "invalid_refresh", "refresh token is invalid or expired")
		return
	}
	h := DefaultHandler
	user, err := h.Store.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusUnauthorized, "invalid_refresh", "refresh token is invalid or expired")
		return
	}
	pair, err := auth.GenerateToken(user.ID, user.Email)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create tokens")
		return
	}
	next, err := refreshSession(pair)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create session")
		return
	}
	hash := auth.HashSessionToken(req.Token)
	rotated, err := h.Store.RotateAuthSession(r.Context(), hash, next)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to rotate session")
		return
	}
	if !rotated {
		// A previously consumed credential signals replay. Revoke persisted
		// sessions for this user, including any successor from a concurrent use.
		old, lookupErr := h.Store.GetAuthSessionByHash(r.Context(), hash)
		if lookupErr == nil && old.RevokedAt != nil {
			if err := h.Store.RevokeAuthSessionsByUser(r.Context(), user.ID, ""); err != nil {
				response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to revoke replayed session")
				return
			}
			RecordAuthEvent(r, user.ID, "auth.refresh_replay", "warning", http.StatusUnauthorized)
		}
		response.WriteError(w, http.StatusUnauthorized, "invalid_refresh", "refresh token was already used; sign in again")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: pair})
}

// issueMFAChallenge persists a password-step credential before exposing it.
// The mfa purpose cannot authenticate API requests or rotate a refresh token.
func issueMFAChallenge(ctx context.Context, user *models.User) (string, error) {
	token, err := auth.GenerateMFAChallenge(user.ID, user.Email, user.CredentialVersion)
	if err != nil {
		return "", err
	}
	claims, err := auth.ValidateMFAChallenge(token)
	if err != nil {
		return "", err
	}
	err = DefaultHandler.Store.CreateAuthSession(ctx, &models.AuthSession{
		ID: claims.ID, UserID: user.ID, SessionHash: auth.HashSessionToken(token),
		SessionPrefix: "mfa", CreatedAt: time.Now().UTC(), ExpiresAt: claims.ExpiresAt.Time,
	})
	if err != nil {
		return "", err
	}
	return token, nil
}
