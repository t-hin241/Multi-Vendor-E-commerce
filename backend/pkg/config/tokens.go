package config

import (
	"fmt"
	"time"

	"shopee/backend/pkg/authjwt"
)

// legacyHS256MaxWindow bounds JWT_ACCEPT_LEGACY_HS256_UNTIL: it exists for
// the minutes of a switch (access tokens live 15 minutes), not as a mode.
const legacyHS256MaxWindow = 24 * time.Hour

// LoadTokenVerifier builds the access-token verifier of a service that
// checks auth: JWT_PUBLIC_KEYS (kid:base64 public key, comma separated).
// It cannot issue tokens. See acceptLegacy for the switch-over window.
func LoadTokenVerifier() (*authjwt.Manager, error) {
	raw, err := requireEnv("JWT_PUBLIC_KEYS")
	if err != nil {
		return nil, err
	}
	keys, err := authjwt.ParseVerifyKeys(raw)
	if err != nil {
		return nil, fmt.Errorf("config: JWT_PUBLIC_KEYS: %w", err)
	}
	m := authjwt.NewVerifier(keys)
	if err := acceptLegacy(m); err != nil {
		return nil, err
	}
	return m, nil
}

// LoadTokenIssuer builds Identity's issuer: JWT_SIGNING_KEY_ID and
// JWT_SIGNING_KEY (base64 Ed25519 seed; generate with
// deploy/gen-jwt-keys.sh). JWT_PUBLIC_KEYS is optional here; when set (a
// rotation), it must agree with the signing key under its id.
func LoadTokenIssuer() (*authjwt.Manager, error) {
	id, err := requireEnv("JWT_SIGNING_KEY_ID")
	if err != nil {
		return nil, err
	}
	seed, err := requireEnv("JWT_SIGNING_KEY")
	if err != nil {
		return nil, err
	}
	signing, err := authjwt.ParseSigningKey(id, seed)
	if err != nil {
		return nil, fmt.Errorf("config: JWT_SIGNING_KEY: %w", err)
	}
	var keys authjwt.VerifyKeys
	if raw := getEnv("JWT_PUBLIC_KEYS", ""); raw != "" {
		if keys, err = authjwt.ParseVerifyKeys(raw); err != nil {
			return nil, fmt.Errorf("config: JWT_PUBLIC_KEYS: %w", err)
		}
	}
	m, err := authjwt.NewIssuer(signing, keys)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := acceptLegacy(m); err != nil {
		return nil, err
	}
	return m, nil
}

// acceptLegacy: while switching from the former shared HS256 secret,
// JWT_ACCEPT_LEGACY_HS256_UNTIL (RFC 3339, at most 24h ahead) with
// JWT_SECRET also accepts tokens signed with that secret. It never signs
// with it, and stops on its own at that time.
func acceptLegacy(m *authjwt.Manager) error {
	until := getEnv("JWT_ACCEPT_LEGACY_HS256_UNTIL", "")
	if until == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, until)
	if err != nil {
		return fmt.Errorf("config: JWT_ACCEPT_LEGACY_HS256_UNTIL must be an RFC 3339 time")
	}
	if time.Until(t) > legacyHS256MaxWindow {
		return fmt.Errorf("config: JWT_ACCEPT_LEGACY_HS256_UNTIL may be at most 24h ahead")
	}
	secret, err := requireEnv("JWT_SECRET")
	if err != nil {
		return fmt.Errorf("config: JWT_ACCEPT_LEGACY_HS256_UNTIL needs the former JWT_SECRET")
	}
	if len(secret) < 32 && getEnv("ENV", "development") == "production" {
		return fmt.Errorf("config: JWT_SECRET must contain at least 32 characters in production")
	}
	m.AcceptLegacyHS256(secret, t)
	return nil
}
