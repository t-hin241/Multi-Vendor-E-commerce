package config_test

import (
	"testing"

	"shopee/backend/pkg/config"
)

func TestLoadBase_RequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	_, err := config.LoadBase("test-service", "8080")
	if err == nil {
		t.Fatal("expected an error when DATABASE_URL is not set, got nil")
	}
}

func TestLoadBase_AppliesDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/test_db?sslmode=disable")
	t.Setenv("PORT", "")
	t.Setenv("ENV", "")

	cfg, err := config.LoadBase("test-service", "9999")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != "9999" {
		t.Errorf("expected default port 9999, got %q", cfg.Port)
	}
	if cfg.Env != "development" {
		t.Errorf("expected default env %q, got %q", "development", cfg.Env)
	}
	if cfg.ServiceName != "test-service" {
		t.Errorf("expected service name %q, got %q", "test-service", cfg.ServiceName)
	}
}

func TestLoadBase_ReadsOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/test_db?sslmode=disable")
	t.Setenv("PORT", "1234")
	t.Setenv("ENV", "production")
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := config.LoadBase("test-service", "9999")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != "1234" {
		t.Errorf("expected overridden port 1234, got %q", cfg.Port)
	}
	if cfg.Env != "production" {
		t.Errorf("expected overridden env production, got %q", cfg.Env)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected overridden log level debug, got %q", cfg.LogLevel)
	}
}
