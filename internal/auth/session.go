package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	SessionPrefix     = "wfs_"
	sessionPrefixSize = 8
)

func GenerateSessionToken() (plaintext, hash, prefix string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", "", fmt.Errorf("generate session entropy: %w", err)
	}
	plaintext = SessionPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return plaintext, HashSessionToken(plaintext), plaintext[:sessionPrefixSize], nil
}

func HashSessionToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

func LooksLikeSessionToken(credential string) bool {
	return strings.HasPrefix(credential, SessionPrefix)
}
