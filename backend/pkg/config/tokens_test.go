package config_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/config"
)

// testKeys returns a base64 seed and its "kid:base64 public key" entry,
// derived from a public label (not secret material).
func testKeys(label string) (seed, public string) {
	s := sha256.Sum256([]byte("config test key " + label))
	pub := ed25519.NewKeyFromSeed(s[:]).Public().(ed25519.PublicKey)
	return base64.StdEncoding.EncodeToString(s[:]), label + ":" + base64.StdEncoding.EncodeToString(pub)
}

func clearTokenEnv(t *testing.T) {
	for _, k := range []string{"JWT_PUBLIC_KEYS", "JWT_SIGNING_KEY", "JWT_SIGNING_KEY_ID", "JWT_ACCEPT_LEGACY_HS256_UNTIL", "JWT_SECRET", "ENV"} {
		t.Setenv(k, "")
	}
}

func TestIssuerTokensVerifyWithThePublishedKeyOnly(t *testing.T) {
	clearTokenEnv(t)
	seed, public := testKeys("k1")
	t.Setenv("JWT_SIGNING_KEY_ID", "k1")
	t.Setenv("JWT_SIGNING_KEY", seed)
	issuer, err := config.LoadTokenIssuer()
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := issuer.IssueSessionToken("u", "buyer", "s", time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("JWT_PUBLIC_KEYS", public)
	verifier, err := config.LoadTokenVerifier()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Parse(token); err != nil {
		t.Fatalf("verifier rejected the issuer's token: %v", err)
	}
	if _, _, err := verifier.IssueAccessToken("u", "admin", time.Minute); !errors.Is(err, authjwt.ErrCannotIssue) {
		t.Fatalf("verifier issued a token: %v", err)
	}

	_, other := testKeys("k2")
	t.Setenv("JWT_PUBLIC_KEYS", other)
	verifier, _ = config.LoadTokenVerifier()
	if _, err := verifier.Parse(token); err == nil {
		t.Fatal("token verified against a key that did not sign it")
	}
}

func TestTokenConfigRejectsBadValues(t *testing.T) {
	clearTokenEnv(t)
	if _, err := config.LoadTokenVerifier(); err == nil {
		t.Fatal("verifier without JWT_PUBLIC_KEYS")
	}
	t.Setenv("JWT_PUBLIC_KEYS", "CHANGE_ME")
	if _, err := config.LoadTokenVerifier(); err == nil {
		t.Fatal("placeholder public key accepted")
	}
	t.Setenv("JWT_SIGNING_KEY_ID", "k1")
	t.Setenv("JWT_SIGNING_KEY", "CHANGE_ME")
	if _, err := config.LoadTokenIssuer(); err == nil {
		t.Fatal("placeholder signing key accepted")
	}
	seed, _ := testKeys("k1")
	_, wrong := testKeys("k2")
	t.Setenv("JWT_SIGNING_KEY", seed)
	t.Setenv("JWT_PUBLIC_KEYS", "k1:"+wrong[len("k2:"):])
	if _, err := config.LoadTokenIssuer(); err == nil {
		t.Fatal("JWT_PUBLIC_KEYS publishing another key under the signing kid was accepted")
	}
}

func TestLegacyHS256WindowIsBounded(t *testing.T) {
	clearTokenEnv(t)
	_, public := testKeys("k1")
	t.Setenv("JWT_PUBLIC_KEYS", public)

	t.Setenv("JWT_ACCEPT_LEGACY_HS256_UNTIL", time.Now().Add(time.Hour).Format(time.RFC3339))
	if _, err := config.LoadTokenVerifier(); err == nil {
		t.Fatal("legacy window without JWT_SECRET")
	}
	t.Setenv("JWT_SECRET", "former-shared-secret-at-least-32-chars")
	if _, err := config.LoadTokenVerifier(); err != nil {
		t.Fatalf("one-hour legacy window refused: %v", err)
	}
	t.Setenv("JWT_ACCEPT_LEGACY_HS256_UNTIL", time.Now().Add(48*time.Hour).Format(time.RFC3339))
	if _, err := config.LoadTokenVerifier(); err == nil {
		t.Fatal("legacy window over 24h accepted")
	}
	t.Setenv("JWT_ACCEPT_LEGACY_HS256_UNTIL", "tomorrow")
	if _, err := config.LoadTokenVerifier(); err == nil {
		t.Fatal("unparsable legacy window accepted")
	}
}
