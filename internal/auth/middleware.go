package auth

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const UserContextKey contextKey = "user"

// Middleware returns a chi-compatible middleware that validates JWT tokens.
// It skips authentication for /api/auth/register and /api/auth/login.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for register and login endpoints
		path := r.URL.Path
		if path == "/api/auth/register" || path == "/api/auth/login" || path == "/api/health" || path == "/api/version" {
			next.ServeHTTP(w, r)
			return
		}

		tokenStr := extractToken(r)
		if tokenStr == "" {
			http.Error(w, `{"error":{"code":"unauthorized","message":"missing token"}}`, http.StatusUnauthorized)
			return
		}

		claims, err := ValidateToken(tokenStr)
		if err != nil {
			http.Error(w, `{"error":{"code":"unauthorized","message":"invalid token"}}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// extractToken gets the JWT from the Authorization header or wolf_token cookie.
func extractToken(r *http.Request) string {
	// Check Authorization header first
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}

	// Check cookie
	cookie, err := r.Cookie("wolf_token")
	if err == nil {
		return cookie.Value
	}

	// Check query parameter (used for direct download links)
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}

	return ""
}

// GetUserFromContext retrieves the authenticated user claims from the request context.
func GetUserFromContext(ctx context.Context) *Claims {
	claims, ok := ctx.Value(UserContextKey).(*Claims)
	if !ok {
		return nil
	}
	return claims
}
