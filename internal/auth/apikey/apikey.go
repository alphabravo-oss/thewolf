// Package apikey implements non-interactive API tokens for the wolf API:
// generation, hashing, and the scope-based authorization model.
//
// A token is "wolf_" + base64url(32 random bytes). Only the SHA-256 hash
// and an 8-char prefix are ever persisted — the plaintext is shown once.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// Prefix is the literal that every token string starts with. It makes a
// leaked token greppable in logs and detectable by secret scanners, and
// lets the auth middleware tell tokens apart from JWTs.
const Prefix = "wolf_"

// DefaultExpiryDays is the token lifetime applied when the caller does
// not specify one. Bounded-by-default nudges automation toward rotation.
const DefaultExpiryDays = 90

// prefixDisplayLen is how many leading characters of a token are stored
// for display (e.g. "wolf_a1B"). Enough to identify, useless to an attacker.
const prefixDisplayLen = 8

// Generate creates a new token. It returns the plaintext secret (shown to
// the user exactly once), its SHA-256 hash (persisted), and a short display
// prefix (persisted).
func Generate() (plaintext, hash, prefix string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", "", fmt.Errorf("generate token entropy: %w", err)
	}
	plaintext = Prefix + base64.RawURLEncoding.EncodeToString(raw)
	return plaintext, Hash(plaintext), plaintext[:prefixDisplayLen], nil
}

// Hash returns the hex-encoded SHA-256 of a token's plaintext.
//
// Plain SHA-256 (not bcrypt/argon2) is correct here: the token is 32 bytes
// of full-entropy randomness, so there is nothing to brute-force. A slow
// hash would only tax every API request for no security gain.
func Hash(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// LooksLikeToken reports whether a credential is an API token (vs. a JWT).
func LooksLikeToken(credential string) bool {
	return strings.HasPrefix(credential, Prefix)
}
