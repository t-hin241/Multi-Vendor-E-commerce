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
	"shopee/backend/pkg/platform/objectstorage"
)

type Config struct {
	Base                     config.Base
	VendorServiceURL         string
	OrderServiceURL          string
	CarrierProvider          string
	CarrierMockWebhookSecret string
	// AddressRetention: how long a final shipment keeps the buyer's contact
	// details; zero keeps them.
	AddressRetention time.Duration
	// DeliveryResolution (AF-04, FEATURE_DELIVERY_RESOLUTION_ENABLED,
	// default off): tell Order about failed deliveries, accept failure
	// reports and redelivery attempts. AttemptLimit failed attempts
	// (SHIPMENT_DELIVERY_ATTEMPT_LIMIT, default 2) open a case.
	DeliveryResolution bool
	AttemptLimit       int
	// ReturnShipping (AF-05, FEATURE_RETURN_SHIPPING_ENABLED, default
	// off): open return parcels Order authorizes. Parcels already open keep
	// moving when it is turned off.
	ReturnShipping bool
	// Evidence (PW-038, SHIPMENT_EVIDENCE_*) is the private bucket of the
	// shared object storage for failure report evidence; nil without
	// SHIPMENT_EVIDENCE_STORAGE_ENDPOINT (no uploads, lost needs none).
	Evidence *objectstorage.PrivateConfig
}

func Load() (Config, error) {
	base, err := config.LoadBase("shipment", "8088")
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

	deliveryResolution, err := strconv.ParseBool(getEnv("FEATURE_DELIVERY_RESOLUTION_ENABLED", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("config: FEATURE_DELIVERY_RESOLUTION_ENABLED must be true or false")
	}
	returnShipping, err := strconv.ParseBool(getEnv("FEATURE_RETURN_SHIPPING_ENABLED", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("config: FEATURE_RETURN_SHIPPING_ENABLED must be true or false")
	}
	attemptLimit, err := strconv.Atoi(getEnv("SHIPMENT_DELIVERY_ATTEMPT_LIMIT", "2"))
	if err != nil || attemptLimit < 1 || attemptLimit > 5 {
		return Config{}, fmt.Errorf("config: SHIPMENT_DELIVERY_ATTEMPT_LIMIT must be between 1 and 5")
	}

	evidence, err := evidenceStorage()
	if err != nil {
		return Config{}, err
	}

	return Config{
		Base: base, VendorServiceURL: vendorServiceURL, OrderServiceURL: orderServiceURL,
		CarrierProvider: carrierProvider, CarrierMockWebhookSecret: carrierMockWebhookSecret,
		AddressRetention:   time.Duration(retentionDays) * 24 * time.Hour,
		DeliveryResolution: deliveryResolution, AttemptLimit: attemptLimit, ReturnShipping: returnShipping,
		Evidence: evidence,
	}, nil
}

func evidenceStorage() (*objectstorage.PrivateConfig, error) {
	endpoint := os.Getenv("SHIPMENT_EVIDENCE_STORAGE_ENDPOINT")
	if endpoint == "" {
		return nil, nil
	}
	private := objectstorage.PrivateConfig{Endpoint: endpoint}
	var err error
	if private.AccessKey, err = requireEnv("SHIPMENT_EVIDENCE_STORAGE_ACCESS_KEY"); err != nil {
		return nil, err
	}
	if private.SecretKey, err = requireEnv("SHIPMENT_EVIDENCE_STORAGE_SECRET_KEY"); err != nil {
		return nil, err
	}
	if private.Bucket, err = requireEnv("SHIPMENT_EVIDENCE_BUCKET"); err != nil {
		return nil, err
	}
	private.UseSSL, _ = strconv.ParseBool(os.Getenv("SHIPMENT_EVIDENCE_STORAGE_USE_SSL"))
	return &private, nil
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
