package auth

import (
	"github.com/golang-jwt/jwt/v5"
	"testing"
	"time"
)

func TestHashAndVerifyPassword(t *testing.T) {
	password := "mySecureP@ssword123"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	if hash == "" {
		t.Fatal("hash should not be empty")
	}

	// Verify correct password
	ok, err := VerifyPassword(password, hash)
	if err != nil {
		t.Fatalf("VerifyPassword failed: %v", err)
	}
	if !ok {
		t.Fatal("password verification should succeed")
	}

	// Verify wrong password
	ok, err = VerifyPassword("wrongpassword", hash)
	if err != nil {
		t.Fatalf("VerifyPassword failed: %v", err)
	}
	if ok {
		t.Fatal("wrong password should not verify")
	}
}

func TestHashUniqueness(t *testing.T) {
	password := "samePassword"
	hash1, _ := HashPassword(password)
	hash2, _ := HashPassword(password)

	if hash1 == hash2 {
		t.Fatal("two hashes of the same password should differ (different salts)")
	}
}

func TestJWTGenerateAndValidate(t *testing.T) {
	SetJWTSecret([]byte("test-secret-key-for-jwt-testing!"))

	pair, err := GenerateToken("user-123", "test@example.com")
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	if pair.AccessToken == "" {
		t.Fatal("access token should not be empty")
	}
	if pair.RefreshToken == "" {
		t.Fatal("refresh token should not be empty")
	}

	// Validate access token
	claims, err := ValidateToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}
	if claims.UserID != "user-123" {
		t.Fatalf("expected user_id user-123, got %s", claims.UserID)
	}
	if claims.Email != "test@example.com" {
		t.Fatalf("expected email test@example.com, got %s", claims.Email)
	}
}

func TestJWTInvalidToken(t *testing.T) {
	SetJWTSecret([]byte("test-secret-key-for-jwt-testing!"))

	_, err := ValidateToken("invalid.token.here")
	if err == nil {
		t.Fatal("should fail for invalid token")
	}
}

func TestJWTRefresh(t *testing.T) {
	SetJWTSecret([]byte("test-secret-key-for-jwt-testing!"))

	pair, err := GenerateToken("user-456", "refresh@example.com")
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	claims, err := ValidateRefreshToken(pair.RefreshToken)
	if err != nil {
		t.Fatalf("RefreshToken failed: %v", err)
	}

	if claims.UserID != "user-456" {
		t.Fatalf("expected user_id user-456, got %s", claims.UserID)
	}
}

func TestTokenPurposesAreNotInterchangeable(t *testing.T) {
	SetJWTSecret([]byte("test-secret-key-for-jwt-testing!"))
	pair, err := GenerateToken("user-123", "test@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateToken(pair.RefreshToken); err == nil {
		t.Fatal("refresh token authenticated as access")
	}
	if _, err := ValidateRefreshToken(pair.AccessToken); err == nil {
		t.Fatal("access token refreshed")
	}
	claims, err := ValidateToken(pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if ttl := claims.ExpiresAt.Sub(claims.IssuedAt.Time); ttl > 15*time.Minute {
		t.Fatalf("access lifetime %s", ttl)
	}
	claims.Purpose = ""
	legacy, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateToken(legacy); err == nil {
		t.Fatal("ambiguous legacy token authenticated")
	}
}
