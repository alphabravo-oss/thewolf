package auth

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func TestMFAChallengeIdentityAndPurpose(t *testing.T) {
	SetJWTSecret([]byte("mfa challenge test signing secret"))
	first, err := GenerateMFAChallenge("user", "user@example.test", 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateMFAChallenge("user", "user@example.test", 0)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("password attempts shared a challenge")
	}
	a, err := ValidateMFAChallenge(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ValidateMFAChallenge(second)
	if err != nil || a.ID == "" || a.ID == b.ID {
		t.Fatalf("challenge identity: %v", err)
	}
	if _, err := ValidateToken(first); err == nil {
		t.Fatal("challenge accepted as access token")
	}
	if _, err := ValidateRefreshToken(first); err == nil {
		t.Fatal("challenge accepted as refresh token")
	}
	version := a.CredentialVersion
	a.CredentialVersion = nil
	unbound, err := jwt.NewWithClaims(jwt.SigningMethodHS256, a).SignedString(jwtSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateMFAChallenge(unbound); err == nil {
		t.Fatal("unbound challenge accepted")
	}
	a.CredentialVersion = version
	a.ID = ""
	legacy, err := jwt.NewWithClaims(jwt.SigningMethodHS256, a).SignedString(jwtSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateMFAChallenge(legacy); err == nil {
		t.Fatal("challenge without identifier accepted")
	}
}
