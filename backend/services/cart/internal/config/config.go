// Package config extends the shared base config with the settings unique to
// Cart: where to reach Catalog (sellability and live prices) and Inventory
// (display-only stock warnings), and how long idle carts and checkout
// operations are kept.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"shopee/backend/pkg/config"
)

// Minimums keep a misconfiguration from purging carts or checkout receipts
// that a buyer or Order's consume retry may still need.
const (
	minCartRetentionDays      = 30
	minOperationRetentionDays = 30
)

type Config struct {
	Base                config.Base
	JWTSecret           string
	CatalogServiceURL   string
	InventoryServiceURL string
	Retention           Retention
}

type Retention struct {
	Enabled      bool
	CartIdle     time.Duration
	OperationTTL time.Duration
	Interval     time.Duration
}

func Load() (Config, error) {
	base, err := config.LoadBase("cart", "8085")
	if err != nil {
		return Config{}, err
	}

	jwtSecret, err := config.RequireJWTSecret()
	if err != nil {
		return Config{}, err
	}

	catalogServiceURL, err := requireEnv("CATALOG_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}
	inventoryServiceURL, err := requireEnv("INVENTORY_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}

	retention, err := loadRetention()
	if err != nil {
		return Config{}, err
	}

	return Config{Base: base, JWTSecret: jwtSecret, CatalogServiceURL: catalogServiceURL,
		InventoryServiceURL: inventoryServiceURL, Retention: retention}, nil
}

func loadRetention() (Retention, error) {
	enabled, err := boolEnv("CART_RETENTION_ENABLED", true)
	if err != nil {
		return Retention{}, err
	}
	cartDays, err := intEnv("CART_RETENTION_DAYS", 180, minCartRetentionDays, 3650)
	if err != nil {
		return Retention{}, err
	}
	operationDays, err := intEnv("CART_OPERATION_RETENTION_DAYS", 90, minOperationRetentionDays, 3650)
	if err != nil {
		return Retention{}, err
	}
	intervalMinutes, err := intEnv("CART_RETENTION_INTERVAL_MINUTES", 60, 5, 1440)
	if err != nil {
		return Retention{}, err
	}
	day := 24 * time.Hour
	return Retention{
		Enabled:      enabled,
		CartIdle:     time.Duration(cartDays) * day,
		OperationTTL: time.Duration(operationDays) * day,
		Interval:     time.Duration(intervalMinutes) * time.Minute,
	}, nil
}

func requireEnv(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", fmt.Errorf("config: required environment variable %q is not set", key)
	}
	return v, nil
}

func intEnv(key string, fallback, minValue, maxValue int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < minValue || v > maxValue {
		return 0, fmt.Errorf("config: %s must be an integer between %d and %d", key, minValue, maxValue)
	}
	return v, nil
}

func boolEnv(key string, fallback bool) (bool, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("config: %s must be true or false", key)
	}
	return v, nil
}
