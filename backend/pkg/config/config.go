// Package config loads process configuration from environment variables.
// Every service reads config exclusively from the environment; no secret
// value is ever hard-coded here.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Base holds the configuration fields every backend service needs.
type Base struct {
	ServiceName string
	Env         string
	Port        string
	LogLevel    string
	DatabaseURL string
	RedisURL    string
	NATSURL     string

	ShutdownTimeout time.Duration
	HTTPReadTimeout time.Duration
	HTTPIdleTimeout time.Duration
}

// LoadBase reads the common configuration shared by all services. It fails
// fast (returns an error) when a required variable is missing so a
// misconfigured service never starts silently.
func LoadBase(serviceName, defaultPort string) (Base, error) {
	databaseURL, err := requireEnv("DATABASE_URL")
	if err != nil {
		return Base{}, err
	}

	return Base{
		ServiceName:     serviceName,
		Env:             getEnv("ENV", "development"),
		Port:            getEnv("PORT", defaultPort),
		LogLevel:        getEnv("LOG_LEVEL", "info"),
		DatabaseURL:     databaseURL,
		RedisURL:        getEnv("REDIS_URL", "redis://localhost:6379/0"),
		NATSURL:         getEnv("NATS_URL", "nats://localhost:4222"),
		ShutdownTimeout: getEnvDuration("SHUTDOWN_TIMEOUT", 10*time.Second),
		HTTPReadTimeout: getEnvDuration("HTTP_READ_TIMEOUT", 5*time.Second),
		HTTPIdleTimeout: getEnvDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
	}, nil
}

// RequireJWTSecret reads the shared access-token signing secret. Only
// services that verify or issue access tokens call this, so services with
// no auth surface (payment, shipment, notification, ...) are not forced to
// carry an unused secret.
func RequireJWTSecret() (string, error) {
	return requireEnv("JWT_SECRET")
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

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}
