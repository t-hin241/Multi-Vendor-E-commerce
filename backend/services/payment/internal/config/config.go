// Package config extends the shared base config with the settings unique to
// Payment: where to reach Order, which provider adapter to run, and that
// adapter's own secrets.
package config

import (
	"fmt"
	"os"

	"shopee/backend/pkg/config"
)

type Config struct {
	Base              config.Base
	JWTSecret         string
	OrderServiceURL   string
	Provider          string
	MockWebhookSecret string
}

func Load() (Config, error) {
	base, err := config.LoadBase("payment", "8087")
	if err != nil {
		return Config{}, err
	}

	jwtSecret, err := config.RequireJWTSecret()
	if err != nil {
		return Config{}, err
	}

	orderServiceURL, err := requireEnv("ORDER_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}

	provider := getEnv("PAYMENT_PROVIDER", "mock")
	if provider != "mock" {
		return Config{}, fmt.Errorf("config: unsupported PAYMENT_PROVIDER %q (only \"mock\" is implemented)", provider)
	}

	mockWebhookSecret, err := requireEnv("PAYMENT_MOCK_WEBHOOK_SECRET")
	if err != nil {
		return Config{}, err
	}

	return Config{
		Base: base, JWTSecret: jwtSecret, OrderServiceURL: orderServiceURL,
		Provider: provider, MockWebhookSecret: mockWebhookSecret,
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
