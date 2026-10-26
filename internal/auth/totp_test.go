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
	if _, ok := ValidateTOTP(code, key.Secret(), time.Now()); !ok {
		t.Error("valid code rejected")
	}
	if _, ok := ValidateTOTP("not-a-code", key.Secret(), time.Now()); ok {
		t.Fatal("invalid code accepted")
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

func TestTOTPStepWindow(t *testing.T) {
	const secret = "JBSWY3DPEHPK3PXP"
	now := time.Unix(1792788000, 0)
	for offset := -2; offset <= 2; offset++ {
		instant := now.Add(time.Duration(offset) * 30 * time.Second)
		code, err := totp.GenerateCode(secret, instant)
		if err != nil {
			t.Fatal(err)
		}
		step, ok := ValidateTOTP(" "+code+" ", secret, now)
		want := offset >= -1 && offset <= 1
		if ok != want || (ok && step != instant.Unix()/30) {
			t.Errorf("offset=%d: step=%d valid=%v", offset, step, ok)
		}
	}
	if _, ok := ValidateTOTP("123456", "invalid!secret", now); ok {
		t.Fatal("invalid secret accepted")
	}
	if _, ok := ValidateTOTP("123456", secret, time.Unix(-1, 0)); ok {
		t.Fatal("negative time accepted")
	}
}
