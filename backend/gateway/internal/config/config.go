// Package config loads the API Gateway's own configuration: which port to
// listen on, which origins are allowed, and where each backend service
// lives.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// Config holds the gateway's routing table and server settings.
type Config struct {
	TrustedProxies []string
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
	"/api/reviews":                   "REVIEW_SERVICE_URL",
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

	origins := splitNonempty(getEnv("ALLOWED_ORIGINS", "http://localhost:3000"))
	env := getEnv("ENV", "development")
	if err := checkOrigins(env, origins); err != nil {
		return Config{}, err
	}

	trusted := splitNonempty(os.Getenv("TRUSTED_PROXY_CIDRS"))
	if err := checkTrustedProxies(env, trusted); err != nil {
		return Config{}, err
	}

	return Config{
		TrustedProxies: trusted,
		Env:            env,
		Port:           getEnv("PORT", "8080"),
		LogLevel:       getEnv("LOG_LEVEL", "info"),
		AllowedOrigins: origins,
		Upstreams:      upstreams,
	}, nil
}

func splitNonempty(raw string) []string {
	var result []string
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// checkOrigins: CORS lists exact origins; production accepts only HTTPS
// origins that are not local (PLT-02).
func checkOrigins(env string, origins []string) error {
	if len(origins) == 0 {
		return fmt.Errorf("gateway config: ALLOWED_ORIGINS is empty")
	}
	for _, o := range origins {
		u, err := url.Parse(o)
		if o == "*" || err != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || strings.Contains(o, "*") {
			return fmt.Errorf("gateway config: ALLOWED_ORIGINS must list exact origins (scheme://host[:port])")
		}
		if env == "production" {
			host := u.Hostname()
			if u.Scheme != "https" || host == "localhost" || host == "127.0.0.1" || strings.HasSuffix(host, ".local") {
				return fmt.Errorf("gateway config: production ALLOWED_ORIGINS must be public https origins, got %q", o)
			}
		}
	}
	return nil
}

// checkTrustedProxies: the gateway takes the client IP (rate limits, logs)
// from X-Forwarded-For only when the request comes from one of these
// networks. Production always sits behind the TLS reverse proxy, so an
// empty list would count every visitor as the proxy's single address, and
// a catch-all range would let anyone forge their IP.
func checkTrustedProxies(env string, cidrs []string) error {
	for _, c := range cidrs {
		if !strings.Contains(c, "/") {
			if net.ParseIP(c) == nil {
				return fmt.Errorf("gateway config: TRUSTED_PROXY_CIDRS entry %q is not an IP or CIDR", c)
			}
			continue
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return fmt.Errorf("gateway config: TRUSTED_PROXY_CIDRS entry %q is not an IP or CIDR", c)
		}
		if ones, _ := n.Mask.Size(); ones == 0 {
			return fmt.Errorf("gateway config: TRUSTED_PROXY_CIDRS must not trust every address (%q)", c)
		}
	}
	if env == "production" && len(cidrs) == 0 {
		return fmt.Errorf("gateway config: production requires TRUSTED_PROXY_CIDRS (the reverse proxy's network)")
	}
	return nil
}
