package domain_test

import (
	"strings"
	"testing"
	"time"

	"shopee/backend/services/identity/internal/domain"
)

// RFC 6238 appendix B (SHA1), truncated to 6 digits.
func TestTOTPMatchesTheRFCVectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037"} {
		if got := domain.TOTPCode(secret, domain.TOTPStepAt(time.Unix(unix, 0))); got != want {
			t.Errorf("t=%d: got %s want %s", unix, got, want)
		}
	}
}

func TestVerifyTOTPWindowAndReplay(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1111111111, 0)
	step := domain.TOTPStepAt(now)
	prev := domain.TOTPCode(secret, step-1)
	if got, ok := domain.VerifyTOTP(secret, prev, now, 0); !ok || got != step-1 {
		t.Fatal("the previous step is accepted (clock drift)")
	}
	if _, ok := domain.VerifyTOTP(secret, prev, now, step-1); ok {
		t.Fatal("a code is used once")
	}
	if _, ok := domain.VerifyTOTP(secret, domain.TOTPCode(secret, step-2), now, 0); ok {
		t.Fatal("two steps back is too old")
	}
	if _, ok := domain.VerifyTOTP(secret, "12345", now, 0); ok {
		t.Fatal("wrong length")
	}
	uri := domain.TOTPURI("Shopee", "admin@example.invalid", secret)
	if !strings.HasPrefix(uri, "otpauth://totp/Shopee:admin@example.invalid?") || !strings.Contains(uri, "secret="+domain.TOTPSecretText(secret)) {
		t.Fatalf("uri %s", uri)
	}
	code, err := domain.NewRecoveryCode()
	if err != nil || !domain.IsRecoveryCode(code) || domain.IsRecoveryCode("123456") {
		t.Fatalf("recovery code %q %v", code, err)
	}
}
