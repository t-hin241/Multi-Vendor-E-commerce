package config_test

import (
	"strings"
	"testing"

	"shopee/backend/pkg/config"
	"shopee/backend/pkg/serviceauth"
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
	t.Setenv("DATABASE_URL", "postgres://catalog_app:0123456789abcdef0123@localhost:5432/test_db?sslmode=disable")
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

func TestInternalServiceIdentity(t *testing.T) {
	key := "fake-order-key-not-a-real-secret-000000"
	shared := "fake-shared-key-not-a-real-secret-00000"
	set := func(env map[string]string) {
		for _, k := range []string{"ENV", "INTERNAL_SERVICE_NAME", "INTERNAL_SERVICE_KEY", "INTERNAL_SERVICE_KEYS", "IDENTITY_SERVICE_KEY", "INTERNAL_AUTH_ACCEPT_SHARED_KEY"} {
			t.Setenv(k, env[k])
		}
	}
	set(map[string]string{"INTERNAL_SERVICE_NAME": "order", "INTERNAL_SERVICE_KEY": key, "INTERNAL_SERVICE_KEYS": "order=" + serviceauth.HashKey(key)})
	in, err := config.LoadInternalServices()
	if err != nil || in.Key != serviceauth.Credential("order", key) || in.Verifier == nil {
		t.Fatalf("named identity: %+v %v", in, err)
	}
	set(map[string]string{"INTERNAL_SERVICE_NAME": "order", "INTERNAL_SERVICE_KEY": key, "INTERNAL_SERVICE_KEYS": "order=" + serviceauth.HashKey("another-key-not-a-real-secret-000000000")})
	if _, err := config.LoadInternalServices(); err == nil {
		t.Fatal("a key that does not match its registered hash is refused")
	}
	set(map[string]string{"ENV": "production", "IDENTITY_SERVICE_KEY": shared})
	if _, err := config.LoadInternalServices(); err == nil {
		t.Fatal("production refuses the shared key alone")
	}
	set(map[string]string{"ENV": "production", "IDENTITY_SERVICE_KEY": shared, "INTERNAL_AUTH_ACCEPT_SHARED_KEY": "true"})
	if in, err := config.LoadInternalServices(); err != nil || in.Key != shared {
		t.Fatalf("rollout window: %v", err)
	}
	set(map[string]string{"ENV": "development", "IDENTITY_SERVICE_KEY": "short"})
	if _, err := config.LoadInternalServices(); err == nil {
		t.Fatal("a short key is refused")
	}
}

func TestDatabaseURLGetsAPoolBudget(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@db:5432/order_db?sslmode=disable")
	t.Setenv("DB_MAX_CONNS", "")
	t.Setenv("DB_STATEMENT_TIMEOUT", "")
	b, err := config.LoadBase("order", "8086")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.DatabaseURL, "pool_max_conns=8") || !strings.Contains(b.DatabaseURL, "statement_timeout=30000") || !strings.Contains(b.DatabaseURL, "sslmode=disable") {
		t.Fatalf("budget not applied: %s", b.DatabaseURL)
	}
	t.Setenv("DATABASE_URL", "postgres://u:p@db:5432/order_db?pool_max_conns=3")
	if b, _ = config.LoadBase("order", "8086"); !strings.Contains(b.DatabaseURL, "pool_max_conns=3") {
		t.Fatal("an explicit setting wins")
	}
	t.Setenv("DB_MAX_CONNS", "500")
	t.Setenv("DATABASE_URL", "postgres://u:p@db:5432/order_db")
	if _, err := config.LoadBase("order", "8086"); err == nil {
		t.Fatal("an absurd pool size is refused")
	}
}

func TestEventBusPassword(t *testing.T) {
	t.Setenv("ENV", "development")
	t.Setenv("EVENTBUS_PASSWORD", "")
	if _, err := config.RequireEventBusPassword(); err == nil {
		t.Fatal("a service on the event bus needs a password")
	}
	// Compose gives the broker and the service the same value, so even the
	// placeholder matches locally; it is used exactly as set.
	t.Setenv("EVENTBUS_PASSWORD", "CHANGE_ME")
	if got, err := config.RequireEventBusPassword(); err != nil || got != "CHANGE_ME" {
		t.Fatalf("development: %q %v", got, err)
	}

	for _, broken := range []string{`with"quote-and-enough-length-000000`, `with\backslash-and-enough-length-00000`} {
		t.Setenv("EVENTBUS_PASSWORD", broken)
		if _, err := config.RequireEventBusPassword(); err == nil {
			t.Errorf("%q would break the broker's quoted password", broken)
		}
	}

	t.Setenv("ENV", "production")
	for _, weak := range []string{"CHANGE_ME", "short-password"} {
		t.Setenv("EVENTBUS_PASSWORD", weak)
		if _, err := config.RequireEventBusPassword(); err == nil {
			t.Errorf("production must refuse %q", weak)
		}
	}
	t.Setenv("EVENTBUS_PASSWORD", strings.Repeat("k", 32))
	if _, err := config.RequireEventBusPassword(); err != nil {
		t.Fatal(err)
	}
}

// Production connects with a generated per-service password, never the
// .env.example placeholder or a short one.
func TestProductionDatabasePassword(t *testing.T) {
	t.Setenv("ENV", "production")
	for _, weak := range []string{"CHANGE_ME", "short"} {
		t.Setenv("DATABASE_URL", "postgres://catalog_app:"+weak+"@postgres:5432/catalog_db?sslmode=disable")
		if _, err := config.LoadBase("catalog", "8083"); err == nil {
			t.Errorf("production accepted database password %q", weak)
		}
	}
	t.Setenv("DATABASE_URL", "postgres://catalog_app@postgres:5432/catalog_db?sslmode=disable")
	if _, err := config.LoadBase("catalog", "8083"); err != nil {
		t.Fatalf("a URL without a password (certificate or PGPASSWORD) is fine: %v", err)
	}
	t.Setenv("ENV", "development")
	t.Setenv("DATABASE_URL", "postgres://catalog_app:CHANGE_ME@postgres:5432/catalog_db?sslmode=disable")
	if _, err := config.LoadBase("catalog", "8083"); err != nil {
		t.Fatalf("development keeps the placeholder: %v", err)
	}
}
