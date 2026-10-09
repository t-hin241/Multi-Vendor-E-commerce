// Package config extends the shared base config with the settings unique to
// Payment: where to reach Order, which provider adapter to run, and that
// adapter's own secrets.
package config

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"shopee/backend/pkg/config"
	"shopee/backend/pkg/platform/objectstorage"
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
	// ManualRefunds is the AF-06 manual bank-transfer refund workflow.
	ManualRefunds ManualRefundConfig
	// VendorActionNotices is FEATURE_VENDOR_ACTION_NOTICES_ENABLED (AF-08):
	// payout results are told to the shop (needs the event bus).
	VendorActionNotices bool
}

// ManualRefundConfig: FEATURE_MANUAL_REFUND_WORKFLOW_ENABLED, the keys that
// seal refund destinations, the claim lease and the private evidence
// bucket.
type ManualRefundConfig struct {
	Enabled bool
	// KeyVersion and Keys (version -> 32-byte key). Empty when no key is
	// configured: destinations can then be neither stored nor read.
	KeyVersion int
	Keys       map[int][]byte
	Lease      time.Duration
	// Evidence is nil without a private bucket: no receipt uploads.
	Evidence *objectstorage.PrivateConfig
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

	manual, err := loadManualRefunds()
	if err != nil {
		return Config{}, err
	}
	vendorNotices, err := strconv.ParseBool(getEnv("FEATURE_VENDOR_ACTION_NOTICES_ENABLED", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("config: FEATURE_VENDOR_ACTION_NOTICES_ENABLED must be true or false")
	}

	return Config{
		AdminApprovals:   approvals,
		ManualRefunds:    manual,
		VendorServiceURL: getEnv("VENDOR_SERVICE_URL", "http://vendor:8082"), WebhookRatePerMinute: rate,
		Base: base, OrderServiceURL: orderServiceURL, VendorActionNotices: vendorNotices,
		Provider: provider, MockWebhookSecret: mockWebhookSecret, PayOSClientID: payosClientID, PayOSAPIKey: payosAPIKey, PayOSChecksumKey: payosChecksumKey, PayOSBaseURL: getEnv("PAYOS_API_URL", "https://api-merchant.payos.vn"), PayOSReturnURL: payosReturnURL, PayOSCancelURL: payosCancelURL,
	}, nil
}

// loadManualRefunds reads AF-06 settings. The destination key is required
// once the workflow is on; with the workflow off a missing or placeholder
// key only disables reading destinations of attempts still open.
func loadManualRefunds() (ManualRefundConfig, error) {
	cfg := ManualRefundConfig{Keys: map[int][]byte{}}
	var err error
	if cfg.Enabled, err = strconv.ParseBool(getEnv("FEATURE_MANUAL_REFUND_WORKFLOW_ENABLED", "false")); err != nil {
		return cfg, fmt.Errorf("config: FEATURE_MANUAL_REFUND_WORKFLOW_ENABLED must be true or false")
	}
	minutes, err := strconv.Atoi(getEnv("MANUAL_REFUND_CLAIM_LEASE_MINUTES", "30"))
	if err != nil || minutes < 5 || minutes > 240 {
		return cfg, fmt.Errorf("config: MANUAL_REFUND_CLAIM_LEASE_MINUTES must be between 5 and 240")
	}
	cfg.Lease = time.Duration(minutes) * time.Minute

	keys, keyErr := destinationKeys()
	switch {
	case keyErr == nil:
		cfg.Keys = keys
		cfg.KeyVersion, _ = strconv.Atoi(getEnv("REFUND_DESTINATION_KEY_VERSION", "1"))
	case cfg.Enabled:
		return cfg, keyErr
	}

	endpoint := os.Getenv("REFUND_EVIDENCE_STORAGE_ENDPOINT")
	if endpoint == "" {
		return cfg, nil
	}
	private := objectstorage.PrivateConfig{Endpoint: endpoint}
	if private.AccessKey, err = requireEnv("REFUND_EVIDENCE_STORAGE_ACCESS_KEY"); err != nil {
		return cfg, err
	}
	if private.SecretKey, err = requireEnv("REFUND_EVIDENCE_STORAGE_SECRET_KEY"); err != nil {
		return cfg, err
	}
	if private.Bucket, err = requireEnv("REFUND_EVIDENCE_BUCKET"); err != nil {
		return cfg, err
	}
	private.UseSSL, _ = strconv.ParseBool(os.Getenv("REFUND_EVIDENCE_STORAGE_USE_SSL"))
	cfg.Evidence = &private
	return cfg, nil
}

// destinationKeys reads REFUND_DESTINATION_KEY (current, version
// REFUND_DESTINATION_KEY_VERSION) and REFUND_DESTINATION_PREVIOUS_KEYS
// ("version:base64,..."), each base64 for exactly 32 bytes.
func destinationKeys() (map[int][]byte, error) {
	version, err := strconv.Atoi(getEnv("REFUND_DESTINATION_KEY_VERSION", "1"))
	if err != nil || version < 1 || version > 1000 {
		return nil, fmt.Errorf("config: REFUND_DESTINATION_KEY_VERSION must be a positive integer")
	}
	current, err := decodeKey(os.Getenv("REFUND_DESTINATION_KEY"))
	if err != nil {
		return nil, fmt.Errorf("config: REFUND_DESTINATION_KEY must be base64 for exactly 32 bytes")
	}
	keys := map[int][]byte{version: current}
	for _, part := range strings.Split(os.Getenv("REFUND_DESTINATION_PREVIOUS_KEYS"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		raw, encoded, ok := strings.Cut(part, ":")
		v, err := strconv.Atoi(raw)
		if !ok || err != nil || v < 1 || v == version {
			return nil, fmt.Errorf("config: REFUND_DESTINATION_PREVIOUS_KEYS must be version:base64 pairs other than the current version")
		}
		if keys[v], err = decodeKey(encoded); err != nil {
			return nil, fmt.Errorf("config: REFUND_DESTINATION_PREVIOUS_KEYS version %d must be base64 for exactly 32 bytes", v)
		}
	}
	return keys, nil
}

func decodeKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("invalid key")
	}
	return key, nil
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
