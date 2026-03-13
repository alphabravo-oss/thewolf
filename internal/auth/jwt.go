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

// Claims represents the JWT claims.
type Claims struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

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
