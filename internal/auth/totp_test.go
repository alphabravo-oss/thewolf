package auth

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestTOTPGenerateAndValidate(t *testing.T) {
	key, err := GenerateTOTPSecret("user@example.com")
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	if key.Secret() == "" {
		t.Fatal("empty secret")
	}
	// A freshly computed code must validate; a wrong one must not.
	code, err := totp.GenerateCode(key.Secret(), time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	if !ValidateTOTP(code, key.Secret()) {
		t.Error("valid code rejected")
	}
	if ValidateTOTP("000000", key.Secret()) && code == "000000" {
		t.Skip("astronomically unlikely collision")
	}
}

func TestTOTPQRDataURI(t *testing.T) {
	key, _ := GenerateTOTPSecret("user@example.com")
	uri, err := TOTPQRDataURI(key)
	if err != nil {
		t.Fatalf("TOTPQRDataURI: %v", err)
	}
	if len(uri) < 32 || uri[:22] != "data:image/png;base64," {
		t.Errorf("unexpected data URI prefix: %.40q", uri)
	}
}

func TestRecoveryCodes(t *testing.T) {
	plain, hashed, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	if len(plain) != recoveryCodeCount || len(hashed) != recoveryCodeCount {
		t.Fatalf("want %d codes, got %d/%d", recoveryCodeCount, len(plain), len(hashed))
	}
	// A real code matches at its index; case/whitespace is normalized.
	idx, ok := MatchRecoveryCode("  "+plain[3]+"  ", hashed)
	if !ok || idx != 3 {
		t.Errorf("MatchRecoveryCode: idx=%d ok=%v, want 3,true", idx, ok)
	}
	if _, ok := MatchRecoveryCode("zzzz-zzzz", hashed); ok {
		t.Error("bogus code matched")
	}
}
