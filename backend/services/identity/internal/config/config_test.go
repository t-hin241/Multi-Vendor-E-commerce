package config

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func setupConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://synthetic@localhost/identity_test?sslmode=disable")
	t.Setenv("JWT_SECRET", strings.Repeat("j", 32))
	t.Setenv("IDENTITY_SERVICE_KEY", strings.Repeat("s", 32))
	t.Setenv("IDENTITY_RESET_DELIVERY_KEY", strings.Repeat("d", 32))
	t.Setenv("IDENTITY_RESET_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	t.Setenv("NOTIFICATION_SERVICE_URL", "http://notification:8090")
	t.Setenv("IDENTITY_PASSWORD_RESET_URL", "https://shop.example.invalid/reset-password")
	t.Setenv("IDENTITY_ALLOWED_ORIGINS", "https://shop.example.invalid")
	t.Setenv("IDENTITY_TRUSTED_PROXY_CIDRS", "")
	t.Setenv("ENV", "production")
}
func TestConfigRejectsUnsafeResetSettings(t *testing.T) {
	for _, tt := range []struct{ key, value string }{{"IDENTITY_RESET_ENCRYPTION_KEY", "CHANGE_ME"}, {"IDENTITY_SERVICE_KEY", "CHANGE_ME"}, {"IDENTITY_ALLOWED_ORIGINS", "*"}, {"IDENTITY_ALLOWED_ORIGINS", "http://shop.example.invalid"}, {"IDENTITY_PASSWORD_RESET_URL", "http://shop.example.invalid/reset-password"}, {"IDENTITY_PASSWORD_RESET_URL", "https://shop.example.invalid/reset-password?token=synthetic"}, {"IDENTITY_TRUSTED_PROXY_CIDRS", "*"}} {
		t.Run(tt.key+tt.value, func(t *testing.T) {
			setupConfig(t)
			t.Setenv(tt.key, tt.value)
			if _, err := Load(); err == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
}
func TestConfigValid(t *testing.T) {
	setupConfig(t)
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
}

func TestStagingRequiresHTTPSForSecureCookie(t *testing.T) {
	setupConfig(t)
	t.Setenv("ENV", "staging")
	t.Setenv("IDENTITY_ALLOWED_ORIGINS", "http://shop.example.invalid")
	if _, err := Load(); err == nil {
		t.Fatal("staging accepted HTTP with Secure cookies")
	}
}
