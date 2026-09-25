// Package config extends the shared base config with the settings unique to
// Notification: where to reach Identity to resolve a recipient, and the
// JWT secret admin's read-only view is authenticated with.
package config

import (
	"fmt"
	"os"

	"shopee/backend/pkg/config"
)

type Config struct {
	Base               config.Base
	JWTSecret          string
	IdentityServiceURL string
}

func Load() (Config, error) {
	base, err := config.LoadBase("notification", "8090")
	if err != nil {
		return Config{}, err
	}

	jwtSecret, err := config.RequireJWTSecret()
	if err != nil {
		return Config{}, err
	}

	identityServiceURL, err := requireEnv("IDENTITY_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}

	return Config{Base: base, JWTSecret: jwtSecret, IdentityServiceURL: identityServiceURL}, nil
}

func requireEnv(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", fmt.Errorf("config: required environment variable %q is not set", key)
	}
	return v, nil
}
