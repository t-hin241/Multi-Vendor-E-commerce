package config

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	baseconfig "shopee/backend/pkg/config"
)

type Config struct {
	Base                                                               baseconfig.Base
	JWTSecret, ServiceKey, ResetDeliveryKey, NotificationURL, ResetURL string
	// Internal verifies calling services (PLT-01).
	Internal           baseconfig.InternalServices
	ResetEncryptionKey []byte
	Origins            []string
	TrustedProxies     []string
}

func Load() (Config, error) {
	c := Config{}
	var err error
	c.Base, err = baseconfig.LoadBase("identity", "8081")
	if err != nil {
		return c, err
	}
	if c.Base.Env != "development" && c.Base.Env != "test" && c.Base.Env != "staging" && c.Base.Env != "production" {
		return c, fmt.Errorf("invalid identity ENV")
	}
	c.JWTSecret, err = baseconfig.RequireJWTSecret()
	if err != nil {
		return c, err
	}
	c.ServiceKey = os.Getenv("IDENTITY_SERVICE_KEY")
	c.ResetDeliveryKey = os.Getenv("IDENTITY_RESET_DELIVERY_KEY")
	if (c.ServiceKey != "" && len(c.ServiceKey) < 32) || len(c.ResetDeliveryKey) < 32 {
		return c, fmt.Errorf("identity service and delivery keys must have at least 32 characters")
	}
	if c.ServiceKey == c.ResetDeliveryKey || os.Getenv("INTERNAL_SERVICE_KEY") == c.ResetDeliveryKey {
		return c, fmt.Errorf("service and reset delivery keys must be distinct")
	}
	if c.Internal, err = baseconfig.LoadInternalServices(); err != nil {
		return c, err
	}
	secure := c.Base.Env == "production" || c.Base.Env == "staging"
	if secure && len(c.JWTSecret) < 32 {
		return c, fmt.Errorf("staging/production JWT_SECRET must have at least 32 characters")
	}
	c.ResetEncryptionKey, err = base64.StdEncoding.DecodeString(os.Getenv("IDENTITY_RESET_ENCRYPTION_KEY"))
	if err != nil || len(c.ResetEncryptionKey) != 32 {
		return c, fmt.Errorf("IDENTITY_RESET_ENCRYPTION_KEY must be base64 encoding of 32 bytes")
	}
	c.NotificationURL = strings.TrimRight(os.Getenv("NOTIFICATION_SERVICE_URL"), "/")
	c.ResetURL = os.Getenv("IDENTITY_PASSWORD_RESET_URL")
	for _, raw := range []string{c.NotificationURL, c.ResetURL} {
		u, e := url.Parse(raw)
		if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return c, fmt.Errorf("invalid identity delivery URL")
		}
	}
	reset, _ := url.Parse(c.ResetURL)
	if reset.Fragment != "" || reset.RawQuery != "" || (secure && reset.Scheme != "https") {
		return c, fmt.Errorf("password reset URL must have no query/fragment and use HTTPS in staging/production")
	}
	for _, raw := range strings.Split(os.Getenv("IDENTITY_ALLOWED_ORIGINS"), ",") {
		origin := strings.TrimSpace(raw)
		u, e := url.Parse(origin)
		if e != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || (secure && u.Scheme != "https") {
			return c, fmt.Errorf("IDENTITY_ALLOWED_ORIGINS requires exact origins, HTTPS in staging/production")
		}
		c.Origins = append(c.Origins, origin)
	}
	for _, raw := range strings.Split(os.Getenv("IDENTITY_TRUSTED_PROXY_CIDRS"), ",") {
		if raw = strings.TrimSpace(raw); raw != "" {
			if _, _, e := net.ParseCIDR(raw); e != nil {
				return c, fmt.Errorf("invalid IDENTITY_TRUSTED_PROXY_CIDRS")
			}
			c.TrustedProxies = append(c.TrustedProxies, raw)
		}
	}
	return c, nil
}
