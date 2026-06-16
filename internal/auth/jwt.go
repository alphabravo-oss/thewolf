package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var jwtSecret []byte

// SetJWTSecret sets the JWT signing key. Must be called before GenerateToken/ValidateToken.
func SetJWTSecret(secret []byte) {
	jwtSecret = secret
}

// TokenPair contains an access token and refresh token.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// Claims represents the JWT claims. Role is not signed into the token — it is
// populated per-request by the middleware via RoleResolver so role changes
// take effect immediately, without re-issuing tokens.
type Claims struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"-"`
	// Purpose distinguishes special-purpose tokens from normal session/access
	// tokens. Empty for session tokens; "mfa" for the short-lived challenge
	// issued between password and TOTP verification. ValidateToken rejects any
	// non-empty purpose so a challenge can never be used as a session.
	Purpose string `json:"purpose,omitempty"`
	jwt.RegisteredClaims
}

// mfaChallengePurpose marks the short-lived token minted after a correct
// password but before the second factor is verified.
const mfaChallengePurpose = "mfa"

// mfaChallengeTTL bounds how long a user has to enter their TOTP code.
const mfaChallengeTTL = 5 * time.Minute

// IsAdmin reports whether the resolved principal has the admin role.
func (c *Claims) IsAdmin() bool { return c != nil && c.Role == "admin" }

// GenerateToken creates a new access + refresh token pair.
func GenerateToken(userID, email string) (*TokenPair, error) {
	if len(jwtSecret) == 0 {
		return nil, fmt.Errorf("jwt secret not set")
	}

	accessClaims := Claims{
		UserID: userID,
		Email:  email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   userID,
		},
	}
	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessStr, err := accessToken.SignedString(jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("sign access token: %w", err)
	}

	refreshClaims := Claims{
		UserID: userID,
		Email:  email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   userID,
		},
	}
	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshStr, err := refreshToken.SignedString(jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("sign refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessStr,
		RefreshToken: refreshStr,
	}, nil
}

// ValidateToken validates a JWT token and returns the claims.
func ValidateToken(tokenStr string) (*Claims, error) {
	if len(jwtSecret) == 0 {
		return nil, fmt.Errorf("jwt secret not set")
	}

	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return jwtSecret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	// Special-purpose tokens (e.g. the MFA challenge) are not sessions and must
	// never authenticate a request.
	if claims.Purpose != "" {
		return nil, fmt.Errorf("token is not a session token")
	}

	return claims, nil
}

// GenerateMFAChallenge mints a short-lived token proving the password step
// passed for userID. It is exchanged (with a valid TOTP/recovery code) for a
// real session via the MFA login endpoint; it carries no session privileges.
func GenerateMFAChallenge(userID, email string) (string, error) {
	if len(jwtSecret) == 0 {
		return "", fmt.Errorf("jwt secret not set")
	}
	claims := Claims{
		UserID:  userID,
		Email:   email,
		Purpose: mfaChallengePurpose,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(mfaChallengeTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   userID,
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecret)
}

// ValidateMFAChallenge validates a challenge token and returns its claims. It
// rejects ordinary session tokens (wrong purpose), so the challenge endpoint
// can't be driven with a stolen session.
func ValidateMFAChallenge(tokenStr string) (*Claims, error) {
	if len(jwtSecret) == 0 {
		return nil, fmt.Errorf("jwt secret not set")
	}
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return jwtSecret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse challenge: %w", err)
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid || claims.Purpose != mfaChallengePurpose {
		return nil, fmt.Errorf("invalid mfa challenge")
	}
	return claims, nil
}

// RefreshToken takes a valid refresh token and returns a new token pair.
func RefreshToken(refreshTokenStr string) (*TokenPair, error) {
	claims, err := ValidateToken(refreshTokenStr)
	if err != nil {
		return nil, fmt.Errorf("invalid refresh token: %w", err)
	}
	return GenerateToken(claims.UserID, claims.Email)
}
