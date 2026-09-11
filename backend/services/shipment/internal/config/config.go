// Package config extends the shared base config with the settings unique to
// Shipment: where to reach Vendor and Order to verify ownership before
// letting a vendor manage a shipment.
package config

import (
	"fmt"
	"os"

	"shopee/backend/pkg/config"
)

type Config struct {
	Base                     config.Base
	JWTSecret                string
	VendorServiceURL         string
	OrderServiceURL          string
	CarrierProvider          string
	CarrierMockWebhookSecret string
}

func Load() (Config, error) {
	base, err := config.LoadBase("shipment", "8088")
	if err != nil {
		return Config{}, err
	}

	jwtSecret, err := config.RequireJWTSecret()
	if err != nil {
		return Config{}, err
	}

	vendorServiceURL, err := requireEnv("VENDOR_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}
	orderServiceURL, err := requireEnv("ORDER_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}

	carrierProvider := getEnv("SHIPMENT_CARRIER_PROVIDER", "mock")
	if carrierProvider != "mock" {
		return Config{}, fmt.Errorf("config: unsupported SHIPMENT_CARRIER_PROVIDER %q (only \"mock\" is implemented)", carrierProvider)
	}

	carrierMockWebhookSecret, err := requireEnv("SHIPMENT_CARRIER_MOCK_WEBHOOK_SECRET")
	if err != nil {
		return Config{}, err
	}

	return Config{
		Base: base, JWTSecret: jwtSecret, VendorServiceURL: vendorServiceURL, OrderServiceURL: orderServiceURL,
		CarrierProvider: carrierProvider, CarrierMockWebhookSecret: carrierMockWebhookSecret,
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
