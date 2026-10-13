package config

import (
	"testing"
	"time"
)

func TestAccountConfig(t *testing.T) {
	setupConfig(t)
	t.Setenv("FEATURE_ACCOUNT_SELF_SERVICE_ENABLED", "true")
	t.Setenv("IDENTITY_EMAIL_VERIFY_URL", "https://shop.example.invalid/verify-email")
	t.Setenv("IDENTITY_EMAIL_CHANGE_URL", "https://shop.example.invalid/change-email")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.AccountSelfService || c.EmailVerifyTTL != 30*time.Minute || c.AccountTokenRetention != 24*time.Hour {
		t.Fatal("incorrect defaults")
	}
	for _, tc := range []struct{ key, value string }{{"FEATURE_ACCOUNT_SELF_SERVICE_ENABLED", "yes"}, {"EMAIL_VERIFY_TTL", "48h"}, {"EMAIL_VERIFY_TTL", "0"}, {"ACCOUNT_TOKEN_RETENTION", "30m"}, {"IDENTITY_EMAIL_CHANGE_URL", "http://shop.example.invalid/change-email"}, {"IDENTITY_EMAIL_CHANGE_URL", "https://shop.example.invalid/change-email?token=synthetic"}, {"IDENTITY_EMAIL_VERIFY_URL", ""}} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
}
