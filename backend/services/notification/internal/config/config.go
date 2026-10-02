// Package config extends the shared base config with Notification's
// settings: Identity (recipients, reset messages, admin checks), the email
// provider and the delivery queue (Asynq on the shared REDIS_URL). Every
// secret is read here only.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"shopee/backend/pkg/config"
	"shopee/backend/pkg/serviceauth"
)

type Config struct {
	Base               config.Base
	JWTSecret          string
	IdentityServiceURL string
	// IdentityServiceKey authenticates internal calls both ways: producers
	// queueing a notification, and Notification reading recipients.
	IdentityServiceKey string
	// InternalVerifier checks which service calls the internal routes.
	InternalVerifier                                                           *serviceauth.Verifier
	ResetDeliveryKey, SMTPHost, SMTPPort, SMTPUsername, SMTPPassword, SMTPFrom string
	SMTPAllowPlaintext                                                         bool
	// EmailProvider is "smtp" or "mock" (local only).
	EmailProvider string
	// AttemptRetention: how long attempt history is kept (0 keeps it).
	AttemptRetention time.Duration
	// DeliveryPaused stops sending while still recording requests.
	DeliveryPaused bool
	// WorkerConcurrency is how many delivery jobs run at once.
	WorkerConcurrency int
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
	internal, err := config.LoadInternalServices()
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
	smtpHost, smtpFrom := os.Getenv("SMTP_HOST"), os.Getenv("SMTP_FROM")
	if base.Env == "production" && (plain || smtpHost == "" || smtpFrom == "") {
		return Config{}, fmt.Errorf("production email requires SMTP with TLS and a sender configured")
	}

	provider := os.Getenv("NOTIFICATION_EMAIL_PROVIDER")
	if provider == "" {
		provider = "mock"
		if smtpHost != "" {
			provider = "smtp"
		}
	}
	switch {
	case provider != "smtp" && provider != "mock":
		return Config{}, fmt.Errorf("config: NOTIFICATION_EMAIL_PROVIDER must be smtp or mock")
	case provider == "mock" && base.Env == "production":
		return Config{}, fmt.Errorf("config: NOTIFICATION_EMAIL_PROVIDER=mock is forbidden in production")
	case provider == "smtp" && (smtpHost == "" || smtpFrom == ""):
		return Config{}, fmt.Errorf("config: NOTIFICATION_EMAIL_PROVIDER=smtp needs SMTP_HOST and SMTP_FROM")
	}

	days := 90
	if raw := os.Getenv("NOTIFICATION_ATTEMPT_RETENTION_DAYS"); raw != "" {
		days, err = strconv.Atoi(raw)
		if err != nil || (days != 0 && (days < 7 || days > 3650)) {
			return Config{}, fmt.Errorf("config: NOTIFICATION_ATTEMPT_RETENTION_DAYS must be 0 or between 7 and 3650")
		}
	}

	concurrency := 10
	if raw := os.Getenv("NOTIFICATION_WORKER_CONCURRENCY"); raw != "" {
		concurrency, err = strconv.Atoi(raw)
		if err != nil || concurrency < 1 || concurrency > 100 {
			return Config{}, fmt.Errorf("config: NOTIFICATION_WORKER_CONCURRENCY must be between 1 and 100")
		}
	}

	return Config{
		Base: base, JWTSecret: jwtSecret, IdentityServiceURL: identityServiceURL, IdentityServiceKey: internal.Key, InternalVerifier: internal.Verifier,
		ResetDeliveryKey: key, SMTPHost: smtpHost, SMTPPort: port, SMTPUsername: os.Getenv("SMTP_USERNAME"),
		SMTPPassword: os.Getenv("SMTP_PASSWORD"), SMTPFrom: smtpFrom, SMTPAllowPlaintext: plain,
		EmailProvider: provider, AttemptRetention: time.Duration(days) * 24 * time.Hour,
		DeliveryPaused: os.Getenv("NOTIFICATION_DELIVERY_PAUSED") == "true", WorkerConcurrency: concurrency,
	}, nil
}

func requireEnv(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", fmt.Errorf("config: required environment variable %q is not set", key)
	}
	return v, nil
}
