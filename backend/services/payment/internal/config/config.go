// Package config extends the shared base config with the settings unique to
// Payment: where to reach Order, which provider adapter to run, and that
// adapter's own secrets.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"

	"shopee/backend/pkg/config"
)

type Config struct {
	Base              config.Base
	OrderServiceURL   string
	Provider          string
	MockWebhookSecret string
	PayOSClientID     string
	PayOSAPIKey       string
	PayOSChecksumKey  string
	PayOSBaseURL      string
	PayOSReturnURL    string
	PayOSCancelURL    string
	// VendorServiceURL resolves verified payout destinations for payouts.
	VendorServiceURL string
	// WebhookRatePerMinute bounds webhook deliveries per client IP.
	WebhookRatePerMinute int64
	// AdminApprovals is FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED (AF-19):
	// manual refund/payout results and ledger adjustments need a second admin.
	AdminApprovals bool
}

func Load() (Config, error) {
	base, err := config.LoadBase("payment", "8087")
	if err != nil {
		return Config{}, err
	}

	orderServiceURL, err := requireEnv("ORDER_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}

	provider := getEnv("PAYMENT_PROVIDER", "mock")
	if provider != "mock" && provider != "payos" {
		return Config{}, fmt.Errorf("config: unsupported PAYMENT_PROVIDER %q", provider)
	}
	if base.Env == "production" && provider == "mock" {
		return Config{}, fmt.Errorf("config: PAYMENT_PROVIDER=mock is forbidden in production")
	}
	var mockWebhookSecret, payosClientID, payosAPIKey, payosChecksumKey, payosReturnURL, payosCancelURL string
	if provider == "mock" {
		mockWebhookSecret, err = requireEnv("PAYMENT_MOCK_WEBHOOK_SECRET")
		if err != nil {
			return Config{}, err
		}
	} else {
		for _, target := range []struct {
			key string
			dst *string
		}{{"PAYOS_CLIENT_ID", &payosClientID}, {"PAYOS_API_KEY", &payosAPIKey}, {"PAYOS_CHECKSUM_KEY", &payosChecksumKey}, {"PAYOS_RETURN_URL", &payosReturnURL}, {"PAYOS_CANCEL_URL", &payosCancelURL}} {
			value, loadErr := requireEnv(target.key)
			if loadErr != nil {
				return Config{}, loadErr
			}
			*target.dst = value
		}
	}

	if base.Env == "production" && provider == "payos" {
		for name, raw := range map[string]string{"PAYOS_RETURN_URL": payosReturnURL, "PAYOS_CANCEL_URL": payosCancelURL, "PAYOS_API_URL": getEnv("PAYOS_API_URL", "https://api-merchant.payos.vn")} {
			if u, err := url.Parse(raw); err != nil || u.Scheme != "https" || u.Host == "" {
				return Config{}, fmt.Errorf("config: %s must be an https URL in production", name)
			}
		}
	}
	rate, err := strconv.ParseInt(getEnv("PAYMENT_WEBHOOK_RATE_PER_MINUTE", "600"), 10, 64)
	if err != nil || rate < 10 || rate > 100000 {
		return Config{}, fmt.Errorf("config: PAYMENT_WEBHOOK_RATE_PER_MINUTE must be between 10 and 100000")
	}

	approvals, err := strconv.ParseBool(getEnv("FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("config: FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED must be true or false")
	}

	return Config{
		AdminApprovals:   approvals,
		VendorServiceURL: getEnv("VENDOR_SERVICE_URL", "http://vendor:8082"), WebhookRatePerMinute: rate,
		Base: base, OrderServiceURL: orderServiceURL,
		Provider: provider, MockWebhookSecret: mockWebhookSecret, PayOSClientID: payosClientID, PayOSAPIKey: payosAPIKey, PayOSChecksumKey: payosChecksumKey, PayOSBaseURL: getEnv("PAYOS_API_URL", "https://api-merchant.payos.vn"), PayOSReturnURL: payosReturnURL, PayOSCancelURL: payosCancelURL,
	}, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func requireEnv(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", fmt.Errorf("config: required environment variable %q is not set", key)
	}
	return v, nil
}
