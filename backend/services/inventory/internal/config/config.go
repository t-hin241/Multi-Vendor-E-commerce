// Package config extends the shared base config with the settings unique to
// Inventory: where to reach Vendor and Catalog for ownership checks.
package config

import (
	"fmt"
	"os"

	"shopee/backend/pkg/config"
)

type Config struct {
	Base              config.Base
	JWTSecret         string
	VendorServiceURL  string
	CatalogServiceURL string
}

func Load() (Config, error) {
	base, err := config.LoadBase("inventory", "8084")
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

	catalogServiceURL, err := requireEnv("CATALOG_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}

	return Config{
		Base:              base,
		JWTSecret:         jwtSecret,
		VendorServiceURL:  vendorServiceURL,
		CatalogServiceURL: catalogServiceURL,
	}, nil
}

func requireEnv(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", fmt.Errorf("config: required environment variable %q is not set", key)
	}
	return v, nil
}
