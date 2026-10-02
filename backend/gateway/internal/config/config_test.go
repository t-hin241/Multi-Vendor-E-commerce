package config_test

import (
	"testing"

	"shopee/backend/gateway/internal/config"
)

func requiredUpstreamEnvVars() []string {
	return []string{
		"IDENTITY_SERVICE_URL", "VENDOR_SERVICE_URL", "CATALOG_SERVICE_URL",
		"INVENTORY_SERVICE_URL", "CART_SERVICE_URL", "ORDER_SERVICE_URL",
		"PAYMENT_SERVICE_URL", "SHIPMENT_SERVICE_URL", "ADMIN_SERVICE_URL",
		"NOTIFICATION_SERVICE_URL", "REVIEW_SERVICE_URL",
	}
}

func setAllUpstreams(t *testing.T) {
	t.Helper()
	for _, envVar := range requiredUpstreamEnvVars() {
		t.Setenv(envVar, "http://localhost:9000")
	}
}

func TestLoad_FailsFastWhenAnUpstreamIsMissing(t *testing.T) {
	setAllUpstreams(t)
	t.Setenv("IDENTITY_SERVICE_URL", "")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected an error when a required upstream URL is missing, got nil")
	}
}

func TestLoad_BuildsFullUpstreamTable(t *testing.T) {
	setAllUpstreams(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Three prefixes route to a service also reached under a shorter prefix
	// (/api/payments + /api/webhooks both to PAYMENT_SERVICE_URL,
	// /api/webhooks/shipment-carrier more specifically than /api/shipments
	// to SHIPMENT_SERVICE_URL), so the prefix count is two more than the
	// number of distinct upstream env vars.
	wantPrefixes := len(requiredUpstreamEnvVars()) + 2
	if len(cfg.Upstreams) != wantPrefixes {
		t.Errorf("expected %d upstream routes, got %d", wantPrefixes, len(cfg.Upstreams))
	}
	if cfg.Upstreams["/api/auth"] != "http://localhost:9000" {
		t.Errorf("expected /api/auth to route to identity service, got %q", cfg.Upstreams["/api/auth"])
	}
	if cfg.Upstreams["/api/payments"] != "http://localhost:9000" {
		t.Errorf("expected /api/payments to route to the payment service, got %q", cfg.Upstreams["/api/payments"])
	}
	if cfg.Upstreams["/api/webhooks"] != "http://localhost:9000" {
		t.Errorf("expected /api/webhooks to route to the payment service, got %q", cfg.Upstreams["/api/webhooks"])
	}
	if cfg.Upstreams["/api/webhooks/shipment-carrier"] != "http://localhost:9000" {
		t.Errorf("expected /api/webhooks/shipment-carrier to route to the shipment service, got %q", cfg.Upstreams["/api/webhooks/shipment-carrier"])
	}
	if cfg.Upstreams["/api/reviews"] != "http://localhost:9000" {
		t.Errorf("expected /api/reviews to route to review service, got %q", cfg.Upstreams["/api/reviews"])
	}
}

func TestProductionOriginsMustBePublicHTTPS(t *testing.T) {
	setAllUpstreams(t)
	t.Setenv("ENV", "production")
	t.Setenv("TRUSTED_PROXY_CIDRS", "192.168.250.0/28")
	for _, bad := range []string{"*", "http://shop.example.com", "https://localhost:3000", "https://*.example.com", "https://shop.example.com/path"} {
		t.Setenv("ALLOWED_ORIGINS", bad)
		if _, err := config.Load(); err == nil {
			t.Errorf("production must refuse origin %q", bad)
		}
	}
	t.Setenv("ALLOWED_ORIGINS", "https://shop.example.com,https://admin.example.com")
	if _, err := config.Load(); err != nil {
		t.Fatalf("public https origins are fine: %v", err)
	}
}

// Behind the TLS proxy the client IP comes from X-Forwarded-For; production
// must name the proxy's network, and no setting may trust everyone.
func TestTrustedProxies(t *testing.T) {
	setAllUpstreams(t)
	t.Setenv("ENV", "production")
	t.Setenv("ALLOWED_ORIGINS", "https://shop.example.com")
	for _, bad := range []string{"", "0.0.0.0/0", "::/0", "10.0.0.0/8,0.0.0.0/0", "proxy", "10.0.0.0/33"} {
		t.Setenv("TRUSTED_PROXY_CIDRS", bad)
		if _, err := config.Load(); err == nil {
			t.Errorf("production must refuse TRUSTED_PROXY_CIDRS=%q", bad)
		}
	}
	t.Setenv("TRUSTED_PROXY_CIDRS", "192.168.250.0/28, 10.1.2.3")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("a proxy network is fine: %v", err)
	}
	if len(cfg.TrustedProxies) != 2 {
		t.Fatalf("trusted proxies = %v", cfg.TrustedProxies)
	}

	t.Setenv("ENV", "development")
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:3000")
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	if _, err := config.Load(); err != nil {
		t.Fatalf("development may trust no proxy: %v", err)
	}
}
