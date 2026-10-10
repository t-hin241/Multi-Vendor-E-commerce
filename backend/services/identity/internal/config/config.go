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
	Base                                                             baseconfig.Base
	ServiceKey, ResetDeliveryKey, RateKey, NotificationURL, ResetURL string
	// Internal verifies calling services (PLT-01).
	Internal           baseconfig.InternalServices
	ResetEncryptionKey []byte
	Origins            []string
	TrustedProxies     []string
	// ScopedAdminPermissions is FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED
	// (AF-19): on, an admin needs an explicit bundle for each admin route.
	ScopedAdminPermissions bool
	// EmailVerification is FEATURE_EMAIL_VERIFICATION_ENABLED (PW-022);
	// VerifyURL (IDENTITY_EMAIL_VERIFY_URL) is the page its links open.
	EmailVerification bool
	VerifyURL         string
	// MFAKey (IDENTITY_MFA_ENCRYPTION_KEY) seals admin authenticator
	// secrets (PW-028); empty turns enrollment off. MFARequired is
	// FEATURE_ADMIN_MFA_REQUIRED: reauthentication needs the code.
	MFAKey      []byte
	MFARequired bool
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
	switch os.Getenv("FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED") {
	case "", "false":
	case "true":
		c.ScopedAdminPermissions = true
	default:
		return c, fmt.Errorf("FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED must be true or false")
	}
	if raw := os.Getenv("IDENTITY_MFA_ENCRYPTION_KEY"); raw != "" && raw != "CHANGE_ME" {
		if c.MFAKey, err = base64.StdEncoding.DecodeString(raw); err != nil || len(c.MFAKey) != 32 {
			return c, fmt.Errorf("IDENTITY_MFA_ENCRYPTION_KEY must be base64 encoding of 32 bytes")
		}
	}
	switch os.Getenv("FEATURE_ADMIN_MFA_REQUIRED") {
	case "", "false":
	case "true":
		if len(c.MFAKey) == 0 {
			return c, fmt.Errorf("FEATURE_ADMIN_MFA_REQUIRED needs IDENTITY_MFA_ENCRYPTION_KEY")
		}
		c.MFARequired = true
	default:
		return c, fmt.Errorf("FEATURE_ADMIN_MFA_REQUIRED must be true or false")
	}
	switch os.Getenv("FEATURE_EMAIL_VERIFICATION_ENABLED") {
	case "", "false":
	case "true":
		c.EmailVerification = true
	default:
		return c, fmt.Errorf("FEATURE_EMAIL_VERIFICATION_ENABLED must be true or false")
	}
	secure := c.Base.Env == "production" || c.Base.Env == "staging"
	// Keys the HMAC that turns email and IP into rate-limit keys, so Redis
	// never holds an email address. Its own secret: it used to be the token
	// signing secret, which only Identity holds now (asymmetric JWT).
	c.RateKey = os.Getenv("IDENTITY_RATE_LIMIT_KEY")
	if c.RateKey == "" || (secure && len(c.RateKey) < 32) {
		return c, fmt.Errorf("IDENTITY_RATE_LIMIT_KEY is required (at least 32 characters in staging/production)")
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
	if c.EmailVerification {
		c.VerifyURL = os.Getenv("IDENTITY_EMAIL_VERIFY_URL")
		v, e := url.Parse(c.VerifyURL)
		if e != nil || v.Host == "" || v.User != nil || v.Fragment != "" || v.RawQuery != "" || (v.Scheme != "http" && v.Scheme != "https") || (secure && v.Scheme != "https") {
			return c, fmt.Errorf("IDENTITY_EMAIL_VERIFY_URL must be an absolute URL without query/fragment (HTTPS in staging/production)")
		}
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
