package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestPayoutConfiguration(t *testing.T) {
	for key, value := range map[string]string{
		"ENV": "development", "DATABASE_URL": "postgres://test_user@localhost/vendor_test?sslmode=disable",
		"IDENTITY_SERVICE_KEY":      "synthetic-test-identity-key-not-for-use",
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

func TestShopStaffConfiguration(t *testing.T) {
	const internal, payout = "synthetic-test-identity-key-not-for-use", "synthetic-test-payout-key-not-for-use"
	good := "synthetic-test-staff-fingerprint-key-0001"
	for _, tc := range []struct {
		name, env, enabled, key, url string
		valid                        bool
	}{
		{"off without settings", "development", "", "", "", true},
		{"off with placeholders", "production", "false", "CHANGE_ME", "http://localhost:3000/accept", true},
		{"on", "development", "true", good, "http://localhost:3000/staff-invitations/accept", true},
		{"on needs key", "development", "true", "", "http://localhost:3000/staff-invitations/accept", false},
		{"placeholder key", "development", "true", "CHANGE_ME", "http://localhost:3000/accept", false},
		{"key reused", "development", "true", internal, "http://localhost:3000/accept", false},
		{"https in production", "production", "true", good, "http://shop.example.invalid/accept", false},
		{"no own fragment", "development", "true", good, "http://localhost:3000/accept#x", false},
		{"no query", "production", "true", good, "https://shop.example.invalid/accept?token=1", false},
		{"bad flag", "development", "maybe", good, "http://localhost:3000/accept", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FEATURE_SHOP_STAFF_ENABLED", tc.enabled)
			t.Setenv("STAFF_INVITATION_FINGERPRINT_KEY", tc.key)
			t.Setenv("STAFF_INVITATION_ACCEPT_URL", tc.url)
			enabled, _, key, _, err := loadShopStaff(tc.env, internal, payout)
			if (err == nil) != tc.valid {
				t.Fatalf("unexpected staff configuration validation: %v", err)
			}
			if err != nil && tc.key != "" && strings.Contains(err.Error(), tc.key) {
				t.Fatal("configuration error disclosed key")
			}
			if tc.valid && enabled && string(key) != tc.key {
				t.Fatal("fingerprint key not loaded")
			}
		})
	}
}
