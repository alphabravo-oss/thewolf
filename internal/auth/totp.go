package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"image/png"
	"strings"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// totpIssuer is the label authenticator apps show next to the account.
const totpIssuer = "Wolf"

// recoveryCodeCount is how many one-time recovery codes we mint at enrollment.
const recoveryCodeCount = 10

// GenerateTOTPSecret creates a fresh TOTP key bound to the user's email. The
// returned key exposes the base32 secret (Secret()) to persist and the
// otpauth:// URL / QR image for enrollment.
func GenerateTOTPSecret(accountName string) (*otp.Key, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: accountName,
	})
	if err != nil {
		return nil, fmt.Errorf("generate totp secret: %w", err)
	}
	return key, nil
}

// TOTPQRDataURI renders the key's provisioning QR code as a PNG data URI so the
// frontend can show it directly in an <img src> with no client-side QR library.
func TOTPQRDataURI(key *otp.Key) (string, error) {
	img, err := key.Image(220, 220)
	if err != nil {
		return "", fmt.Errorf("render qr: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("encode qr png: %w", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// ValidateTOTP reports whether code is a currently-valid 6-digit token for the
// given base32 secret (with the library's default ±1 step skew tolerance).
func ValidateTOTP(code, secret string) bool {
	return totp.Validate(strings.TrimSpace(code), secret)
}

// GenerateRecoveryCodes mints recoveryCodeCount single-use codes. It returns
// the plaintext codes (shown to the user exactly once) and their SHA-256
// hashes (persisted). The codes are high-entropy random values, so a fast hash
// is appropriate — unlike user passwords, they are not guessable.
func GenerateRecoveryCodes() (plain []string, hashed []string, err error) {
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	for i := 0; i < recoveryCodeCount; i++ {
		b := make([]byte, 5) // 40 bits -> 8 base32 chars
		if _, err = rand.Read(b); err != nil {
			return nil, nil, fmt.Errorf("read random: %w", err)
		}
		s := strings.ToLower(enc.EncodeToString(b))
		code := s[:4] + "-" + s[4:] // e.g. "a1b2-c3d4"
		plain = append(plain, code)
		hashed = append(hashed, HashRecoveryCode(code))
	}
	return plain, hashed, nil
}

// HashRecoveryCode returns the SHA-256 hex digest of a normalized recovery code.
func HashRecoveryCode(code string) string {
	norm := strings.ToLower(strings.TrimSpace(code))
	sum := sha256.Sum256([]byte(norm))
	return fmt.Sprintf("%x", sum)
}

// MatchRecoveryCode constant-time compares code against a list of stored
// hashes. It returns the index of the matched hash so the caller can consume
// (remove) it, and ok=false if none match.
func MatchRecoveryCode(code string, hashes []string) (int, bool) {
	want := HashRecoveryCode(code)
	for i, h := range hashes {
		if subtle.ConstantTimeCompare([]byte(h), []byte(want)) == 1 {
			return i, true
		}
	}
	return -1, false
}
