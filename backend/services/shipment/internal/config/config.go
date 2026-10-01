// Package config extends the shared base config with the settings unique to
// Shipment: where to reach Vendor and Order to verify ownership before
// letting a vendor manage a shipment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"shopee/backend/pkg/config"
)

type Config struct {
	Base                     config.Base
	JWTSecret                string
	VendorServiceURL         string
	OrderServiceURL          string
	CarrierProvider          string
	CarrierMockWebhookSecret string
	// AddressRetention: how long a final shipment keeps the buyer's contact
	// details; zero keeps them.
	AddressRetention time.Duration
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

	// manual (default, D04): tracking is entered by vendors/admins and an
	// interception is resolved by an operator. mock: a simulated carrier
	// with signed webhooks, for local testing only.
	carrierProvider := getEnv("SHIPMENT_CARRIER_PROVIDER", "manual")
	if carrierProvider != "manual" && carrierProvider != "mock" {
		return Config{}, fmt.Errorf("config: unsupported SHIPMENT_CARRIER_PROVIDER %q (manual or mock)", carrierProvider)
	}
	if carrierProvider == "mock" && base.Env == "production" {
		return Config{}, fmt.Errorf("config: SHIPMENT_CARRIER_PROVIDER=mock is forbidden in production")
	}
	var carrierMockWebhookSecret string
	if carrierProvider == "mock" {
		if carrierMockWebhookSecret, err = requireEnv("SHIPMENT_CARRIER_MOCK_WEBHOOK_SECRET"); err != nil {
			return Config{}, err
		}
	}

	retentionDays, err := strconv.Atoi(getEnv("SHIPMENT_ADDRESS_RETENTION_DAYS", "180"))
	if err != nil || (retentionDays != 0 && (retentionDays < 30 || retentionDays > 3650)) {
		return Config{}, fmt.Errorf("config: SHIPMENT_ADDRESS_RETENTION_DAYS must be 0 (keep) or between 30 and 3650")
	}

	return Config{
		Base: base, JWTSecret: jwtSecret, VendorServiceURL: vendorServiceURL, OrderServiceURL: orderServiceURL,
		CarrierProvider: carrierProvider, CarrierMockWebhookSecret: carrierMockWebhookSecret,
		AddressRetention: time.Duration(retentionDays) * 24 * time.Hour,
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
