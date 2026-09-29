package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestPayoutConfiguration(t *testing.T) {
	for key, value := range map[string]string{
		"ENV": "development", "DATABASE_URL": "postgres://test_user@localhost/vendor_test?sslmode=disable",
		"JWT_SECRET": "synthetic-test-jwt-key-not-for-use", "IDENTITY_SERVICE_KEY": "synthetic-test-identity-key-not-for-use",
		"VENDOR_PAYOUT_SERVICE_KEY": "synthetic-test-payout-key-not-for-use",
		"NOTIFICATION_SERVICE_URL":  "http://notification.test", "OBJECT_STORAGE_ENDPOINT": "storage.test",
		"OBJECT_STORAGE_ACCESS_KEY": "synthetic-test-access", "OBJECT_STORAGE_SECRET_KEY": "synthetic-test-secret",
		"OBJECT_STORAGE_BUCKET": "test-bucket", "OBJECT_STORAGE_PUBLIC_BASE_URL": "http://storage.test",
	} {
		t.Setenv(key, value)
	}
	for _, tc := range []struct {
		name, encoded string
		valid         bool
	}{
		{"32 bytes", base64.StdEncoding.EncodeToString(make([]byte, 32)), true},
		{"43 bytes", base64.StdEncoding.EncodeToString(make([]byte, 43)), false},
		{"placeholder", "CHANGE_ME", false},
		{"missing", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VENDOR_PAYOUT_ENCRYPTION_KEY", tc.encoded)
			_, err := Load()
			if (err == nil) != tc.valid {
				t.Fatal("unexpected payout configuration validation", err)
			}
			if err != nil && tc.encoded != "" && strings.Contains(err.Error(), tc.encoded) {
				t.Fatal("configuration error disclosed key")
			}
		})
	}
	t.Setenv("VENDOR_PAYOUT_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("VENDOR_PAYOUT_SERVICE_KEY", "synthetic-test-identity-key-not-for-use")
	if _, err := Load(); err == nil {
		t.Fatal("payout scope shares internal service key")
	}
}
