package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
)

type contextKey string

const (
	// UserContextKey holds the *Claims of the authenticated principal.
	// Kept for backward compatibility with existing handlers.
	UserContextKey contextKey = "user"
	// AuthInfoContextKey holds the *AuthInfo (claims + scopes + token id).
	AuthInfoContextKey contextKey = "authinfo"
)

// AuthInfo is the full authenticated principal for a request: the identity
// (Claims) plus the effective authorization scopes and, when the request
// was authenticated by an API token rather than a JWT, the token's ID.
type AuthInfo struct {
	Claims          *Claims
	Scopes          apikey.ScopeSet
	ScannerPersonas []string
	TokenID         string // empty for JWT/UI sessions
	SessionID       string // empty unless authenticated by the wolf_token cookie
}

// ResolvedToken is the principal an APITokenResolver returns for a valid,
// non-revoked, non-expired API token.
type ResolvedToken struct {
	TokenID string
	UserID  string
	Email   string
	Scopes  []string
}

type ResolvedSession struct {
	SessionID string
	UserID    string
	Email     string
}

// APITokenResolver looks up a plaintext API token. It must return (nil, nil)
// when the token is unknown, revoked, or expired — never a partial result.
type APITokenResolver func(ctx context.Context, plaintext string) (*ResolvedToken, error)

type SessionResolver func(ctx context.Context, plaintext string) (*ResolvedSession, error)

var resolveAPIToken APITokenResolver
var resolveSession SessionResolver

// SetAPITokenResolver wires the API-token lookup. Called once at server
// startup. When unset, only JWT credentials are accepted.
func SetAPITokenResolver(f APITokenResolver) { resolveAPIToken = f }

func SetSessionResolver(f SessionResolver) { resolveSession = f }

// Middleware validates the request credential — either a JWT (the UI) or a
// "wolf_"-prefixed API token (CLI / CI / AI agents) — and attaches the
// resolved principal to the request context.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		cred, source := extractToken(r)
		if cred == "" {
			writeAuthError(w, http.StatusUnauthorized, "unauthorized", "missing credential")
			return
		}

		var info *AuthInfo
		humanSession := false
		if source == credentialCookie && LooksLikeSessionToken(cred) {
			if resolveSession == nil {
				writeAuthError(w, http.StatusUnauthorized, "unauthorized", "browser sessions are not enabled")
				return
			}
			session, err := resolveSession(r.Context(), cred)
			if err != nil || session == nil {
				writeAuthError(w, http.StatusUnauthorized, "unauthorized", "invalid, revoked, or expired session")
				return
			}
			info = &AuthInfo{
				Claims:    &Claims{UserID: session.UserID, Email: session.Email},
				SessionID: session.SessionID,
			}
			humanSession = true
		} else if apikey.LooksLikeToken(cred) {
			if resolveAPIToken == nil {
				writeAuthError(w, http.StatusUnauthorized, "unauthorized", "API tokens are not enabled")
				return
			}
			rt, err := resolveAPIToken(r.Context(), cred)
			if err != nil || rt == nil {
				writeAuthError(w, http.StatusUnauthorized, "unauthorized", "invalid, revoked, or expired API token")
				return
			}
			info = &AuthInfo{
				Claims:  &Claims{UserID: rt.UserID, Email: rt.Email},
				Scopes:  apikey.ScopeSet(rt.Scopes),
				TokenID: rt.TokenID,
			}
		} else {
			claims, err := ValidateToken(cred)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
				return
			}
			info = &AuthInfo{Claims: claims}
			humanSession = true
		}

		// Resolve the principal's role per-request (admin | user) so role
		// changes take effect immediately. Scopes gate API-token capability;
		// Role gates the human admin surface (settings, user/node management,
		// scanner image builds) and cross-owner modification.
		var scannerPersonas []string
		if humanSession && HumanAuthorizationResolver != nil && info.Claims != nil && info.Claims.UserID != "" {
			resolved, err := HumanAuthorizationResolver(r.Context(), info.Claims.UserID)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "unauthorized", "current authorization could not be resolved")
				return
			}
			info.Claims.Role = resolved.Role
			scannerPersonas = resolved.ScannerPersonas
		} else if humanSession && RoleResolver != nil && info.Claims != nil && info.Claims.UserID != "" {
			info.Claims.Role = RoleResolver(r.Context(), info.Claims.UserID)
		}
		if humanSession {
			if info.Claims != nil && info.Claims.IsAdmin() {
				info.Scopes = apikey.AdminAll()
				info.ScannerPersonas = []string{apikey.ScannerPersonaSupplyChainAdministrator}
			} else {
				_, personas, err := apikey.ScannerScopesForPersonas(scannerPersonas)
				if err != nil {
					personas = []string{apikey.ScannerPersonaViewer}
				}
				info.ScannerPersonas = personas
				info.Scopes = apikey.UserSessionForScannerPersonas(personas)
			}
		}

		ctx := context.WithValue(r.Context(), UserContextKey, info.Claims)
		ctx = context.WithValue(ctx, AuthInfoContextKey, info)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RoleResolver, when set, returns a user's role ("admin" | "user") by id. The
// server wires it to the store at startup. Kept as a package var so the auth
// package doesn't depend on the db package.
var RoleResolver func(ctx context.Context, userID string) string

type HumanAuthorization struct {
	Role            string
	ScannerPersonas []string
}

// HumanAuthorizationResolver reloads a human principal's role and scanner
// personas for every request. This makes grants and revocations effective for
// existing browser sessions without token rotation or re-login.
var HumanAuthorizationResolver func(ctx context.Context, userID string) (HumanAuthorization, error)

// RequireAdmin returns middleware that rejects the request with 403 unless the
// authenticated principal has the admin role. Used for the settings/admin
// surface (settings writes, user + node management, scanner image builds).
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetUserFromContext(r.Context())
		if claims == nil {
			writeAuthError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
			return
		}
		if !claims.IsAdmin() {
			writeAuthError(w, http.StatusForbidden, "forbidden", "this action requires an administrator")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireScope returns middleware that rejects the request with 403 unless
// the authenticated principal holds every one of the required scopes.
func RequireScope(required ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info := GetAuthInfo(r.Context())
			if info == nil {
				writeAuthError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
				return
			}
			if !info.Scopes.HasAll(required...) {
				writeAuthError(w, http.StatusForbidden, "insufficient_scope",
					"this endpoint requires scope: "+strings.Join(required, ", "))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isPublicPath reports whether a path is reachable without authentication.
// Matched on suffix so it works regardless of the /api or /api/v1 prefix.
func isPublicPath(path string) bool {
	for _, suffix := range []string{
		"/auth/register", "/auth/login", "/auth/settings", "/auth/providers",
		"/health", "/ready", "/version",
		"/openapi.json", "/docs",
	} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	if strings.Contains(path, "/auth/sso/") || strings.Contains(path, "/scim/v2/") {
		return true
	}
	return strings.Contains(path, "/docs/")
}

func writeAuthError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + msg + `"}}`))
}

// extractToken gets the credential from the Authorization header, the
// wolf_token cookie, or a token query parameter (for direct download links).
type credentialSource int

const (
	credentialNone credentialSource = iota
	credentialBearer
	credentialCookie
	credentialQuery
)

func extractToken(r *http.Request) (string, credentialSource) {
	authz := r.Header.Get("Authorization")
	if strings.HasPrefix(authz, "Bearer ") {
		return strings.TrimPrefix(authz, "Bearer "), credentialBearer
	}
	if cookie, err := r.Cookie("wolf_token"); err == nil {
		return cookie.Value, credentialCookie
	}
	if t := r.URL.Query().Get("token"); t != "" {
		return t, credentialQuery
	}
	return "", credentialNone
}

// GetUserFromContext retrieves the authenticated user claims.
func GetUserFromContext(ctx context.Context) *Claims {
	claims, ok := ctx.Value(UserContextKey).(*Claims)
	if !ok {
		return nil
	}
	return claims
}

// GetAuthInfo retrieves the full authenticated principal (claims + scopes).
func GetAuthInfo(ctx context.Context) *AuthInfo {
	info, ok := ctx.Value(AuthInfoContextKey).(*AuthInfo)
	if !ok {
		return nil
	}
	return info
}

// TokenIDFromContext returns the API token ID for the request, or "" if the
// request was authenticated with a JWT.
func TokenIDFromContext(ctx context.Context) string {
	if info := GetAuthInfo(ctx); info != nil {
		return info.TokenID
	}
	return ""
}
