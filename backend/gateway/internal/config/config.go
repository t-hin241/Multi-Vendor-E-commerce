// Package config loads the API Gateway's own configuration: which port to
// listen on, which origins are allowed, and where each backend service
// lives.
package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds the gateway's routing table and server settings.
type Config struct {
	Env            string
	Port           string
	LogLevel       string
	AllowedOrigins []string
	Upstreams      map[string]string // route prefix -> upstream base URL
}

// upstreamEnvByPrefix maps a public API prefix to the environment variable
// holding the base URL of the service that owns it. This is the phase-0
// routing skeleton: each prefix proxies 1:1 to the service that owns that
// bounded context today; buyer-facing composition across services (BFF
// aggregation) is added once those services have real endpoints.
var upstreamEnvByPrefix = map[string]string{
	"/api/auth":                      "IDENTITY_SERVICE_URL",
	"/api/vendor":                    "VENDOR_SERVICE_URL",
	"/api/catalog":                   "CATALOG_SERVICE_URL",
	"/api/inventory":                 "INVENTORY_SERVICE_URL",
	"/api/cart":                      "CART_SERVICE_URL",
	"/api/orders":                    "ORDER_SERVICE_URL",
	"/api/payments":                  "PAYMENT_SERVICE_URL",
	"/api/webhooks":                  "PAYMENT_SERVICE_URL",
	"/api/webhooks/shipment-carrier": "SHIPMENT_SERVICE_URL",
	"/api/shipments":                 "SHIPMENT_SERVICE_URL",
	"/api/admin":                     "ADMIN_SERVICE_URL",
	"/api/notifications":             "NOTIFICATION_SERVICE_URL",
}

// Load reads the gateway configuration from the environment. It fails fast
// if any upstream service URL is missing, since a gateway that cannot reach
// a service it is responsible for routing to should not start.
func Load() (Config, error) {
	upstreams := make(map[string]string, len(upstreamEnvByPrefix))
	for prefix, envVar := range upstreamEnvByPrefix {
		v, ok := os.LookupEnv(envVar)
		if !ok || v == "" {
			return Config{}, fmt.Errorf("gateway config: required environment variable %q is not set", envVar)
		}
		upstreams[prefix] = strings.TrimRight(v, "/")
	}

	origins := strings.Split(getEnv("ALLOWED_ORIGINS", "http://localhost:3000"), ",")

	return Config{
		Env:            getEnv("ENV", "development"),
		Port:           getEnv("PORT", "8080"),
		LogLevel:       getEnv("LOG_LEVEL", "info"),
		AllowedOrigins: origins,
		Upstreams:      upstreams,
	}, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
