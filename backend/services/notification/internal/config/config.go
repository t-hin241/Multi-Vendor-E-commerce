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
	Base                                                                       config.Base
	JWTSecret                                                                  string
	IdentityServiceURL                                                         string
	ResetDeliveryKey, SMTPHost, SMTPPort, SMTPUsername, SMTPPassword, SMTPFrom string
	SMTPAllowPlaintext                                                         bool
	IdentityServiceKey                                                         string
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

	key := os.Getenv("IDENTITY_RESET_DELIVERY_KEY")
	if len(key) < 32 {
		return Config{}, fmt.Errorf("IDENTITY_RESET_DELIVERY_KEY must have at least 32 characters")
	}
	port := os.Getenv("SMTP_PORT")
	if port == "" {
		port = "587"
	}
	plain := os.Getenv("SMTP_ALLOW_PLAINTEXT") == "true"
	if base.Env == "production" && (plain || os.Getenv("SMTP_HOST") == "" || os.Getenv("SMTP_FROM") == "") {
		return Config{}, fmt.Errorf("production password reset requires SMTP with TLS and sender configured")
	}
	return Config{IdentityServiceKey: os.Getenv("IDENTITY_SERVICE_KEY"), Base: base, JWTSecret: jwtSecret, IdentityServiceURL: identityServiceURL, ResetDeliveryKey: key, SMTPHost: os.Getenv("SMTP_HOST"), SMTPPort: port, SMTPUsername: os.Getenv("SMTP_USERNAME"), SMTPPassword: os.Getenv("SMTP_PASSWORD"), SMTPFrom: os.Getenv("SMTP_FROM"), SMTPAllowPlaintext: plain}, nil
}

func requireEnv(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", fmt.Errorf("config: required environment variable %q is not set", key)
	}
	return v, nil
}
