// Package config extends the shared base config with the settings unique to
// Vendor: the JWT secret and where to reach Notification for approval
// decisions.
package config

import (
	"fmt"
	"os"

	"shopee/backend/pkg/config"
)

type Config struct {
	Base                   config.Base
	JWTSecret              string
	NotificationServiceURL string
}

func Load() (Config, error) {
	base, err := config.LoadBase("vendor", "8082")
	if err != nil {
		return Config{}, err
	}

	jwtSecret, err := config.RequireJWTSecret()
	if err != nil {
		return Config{}, err
	}

	notificationServiceURL, err := requireEnv("NOTIFICATION_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}

	return Config{Base: base, JWTSecret: jwtSecret, NotificationServiceURL: notificationServiceURL}, nil
}

func requireEnv(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", fmt.Errorf("config: required environment variable %q is not set", key)
	}
	return v, nil
}
