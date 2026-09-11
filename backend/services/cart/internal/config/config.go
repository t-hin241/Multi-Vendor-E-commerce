// Package config extends the shared base config with the setting unique to
// Cart: where to reach Catalog to validate products and read live prices.
package config

import (
	"fmt"
	"os"

	"shopee/backend/pkg/config"
)

type Config struct {
	Base              config.Base
	JWTSecret         string
	CatalogServiceURL string
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

	return Config{Base: base, JWTSecret: jwtSecret, CatalogServiceURL: catalogServiceURL}, nil
}

func requireEnv(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", fmt.Errorf("config: required environment variable %q is not set", key)
	}
	return v, nil
}
