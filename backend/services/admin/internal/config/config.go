// Package config loads the Admin service settings: the shared base config,
// the access-token secret and where each domain service is reached. Admin
// only reads from those services; it holds no credential that can change
// their data.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"shopee/backend/pkg/config"
)

// Upstream is one domain service the dashboard and the audit search read.
type Upstream struct {
	Name string
	URL  string
}

type Config struct {
	Base      config.Base
	JWTSecret string
	Upstreams []Upstream
	// UpstreamTimeout bounds each read from a domain service, so one slow
	// service shows as unavailable instead of stalling the page.
	UpstreamTimeout time.Duration
}

// upstreams lists the services in a fixed order. REVIEW_SERVICE_URL is
// optional (reviews may be disabled); every other one is required.
var upstreams = []struct {
	name, env string
	optional  bool
}{
	{"identity", "IDENTITY_SERVICE_URL", false},
	{"vendor", "VENDOR_SERVICE_URL", false},
	{"catalog", "CATALOG_SERVICE_URL", false},
	{"inventory", "INVENTORY_SERVICE_URL", false},
	{"order", "ORDER_SERVICE_URL", false},
	{"payment", "PAYMENT_SERVICE_URL", false},
	{"shipment", "SHIPMENT_SERVICE_URL", false},
	{"notification", "NOTIFICATION_SERVICE_URL", false},
	{"review", "REVIEW_SERVICE_URL", true},
}

func Load() (Config, error) {
	base, err := config.LoadBase("admin", "8089")
	if err != nil {
		return Config{}, err
	}
	secret, err := config.RequireJWTSecret()
	if err != nil {
		return Config{}, err
	}
	cfg := Config{Base: base, JWTSecret: secret, UpstreamTimeout: 4 * time.Second}
	for _, u := range upstreams {
		raw := strings.TrimRight(strings.TrimSpace(os.Getenv(u.env)), "/")
		if raw == "" {
			if u.optional {
				continue
			}
			return Config{}, fmt.Errorf("config: %s is required", u.env)
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" {
			return Config{}, fmt.Errorf("config: invalid %s", u.env)
		}
		cfg.Upstreams = append(cfg.Upstreams, Upstream{Name: u.name, URL: raw})
	}
	if raw := os.Getenv("ADMIN_UPSTREAM_TIMEOUT"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < 500*time.Millisecond || d > 30*time.Second {
			return Config{}, fmt.Errorf("config: ADMIN_UPSTREAM_TIMEOUT must be a duration between 500ms and 30s")
		}
		cfg.UpstreamTimeout = d
	}
	return cfg, nil
}
