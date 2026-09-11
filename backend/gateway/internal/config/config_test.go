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
		"NOTIFICATION_SERVICE_URL",
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
}
