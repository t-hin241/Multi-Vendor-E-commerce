package authjwt_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/authjwt/authjwttest"
)

func TestIssueAndParse_RoundTrips(t *testing.T) {
	manager := authjwttest.Manager()

	token, expiresAt, err := manager.IssueSessionToken("user-1", "buyer", "session-1", time.Minute)
	if err != nil {
		t.Fatalf("unexpected error issuing token: %v", err)
	}
	if expiresAt.Before(time.Now()) {
		t.Fatal("expected expiry to be in the future")
	}

	// A service holding only the public key verifies it.
	claims, err := authjwttest.Verifier().Parse(token)
	if err != nil {
		t.Fatalf("unexpected error parsing token: %v", err)
	}
	if claims.UserID != "user-1" || claims.Role != "buyer" || claims.SessionID != "session-1" || claims.Issuer != authjwt.Issuer {
		t.Errorf("unexpected claims %+v", claims)
	}
}

func TestVerifierCannotIssue(t *testing.T) {
	if _, _, err := authjwttest.Verifier().IssueAccessToken("user-1", "admin", time.Minute); !errors.Is(err, authjwt.ErrCannotIssue) {
		t.Fatalf("a verifier issued a token: %v", err)
	}
}

func TestParse_RejectsExpiredToken(t *testing.T) {
	manager := authjwttest.Manager()

	token, _, err := manager.IssueAccessToken("user-1", "buyer", -time.Minute)
	if err != nil {
		t.Fatalf("unexpected error issuing token: %v", err)
	}

	if _, err := manager.Parse(token); err == nil {
		t.Fatal("expected an error parsing an expired token, got nil")
	}
}

func TestParse_RejectsTokenSignedWithAnotherKey(t *testing.T) {
	token, _, err := authjwttest.OtherManager().IssueAccessToken("user-1", "buyer", time.Minute)
	if err != nil {
		t.Fatalf("unexpected error issuing token: %v", err)
	}
	if _, err := authjwttest.Verifier().Parse(token); err == nil {
		t.Fatal("expected an error parsing a token signed with another key, got nil")
	}
}

func TestParse_RejectsGarbage(t *testing.T) {
	if _, err := authjwttest.Manager().Parse("not-a-jwt"); err == nil {
		t.Fatal("expected an error parsing garbage input, got nil")
	}
}

func claimsFor(role string) authjwt.Claims {
	return authjwt.Claims{UserID: "user-1", Role: role, SessionID: "s", RegisteredClaims: jwt.RegisteredClaims{
		Issuer: authjwt.Issuer, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}}
}

// Forgeries a verifier must refuse, whatever it is configured with.
func TestParse_RejectsForgedTokens(t *testing.T) {
	verifier := authjwttest.Verifier()
	verifier.AcceptLegacyHS256("former-shared-secret-for-the-rollout", time.Now().Add(time.Hour))
	key := authjwttest.SigningKey()

	// alg=none
	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, claimsFor("admin")).SignedString(jwt.UnsafeAllowNoneSignatureType)

	// Algorithm confusion: HS256 keyed with the public key a verifier holds.
	pub := key.Key.Public().(ed25519.PublicKey)
	confused, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claimsFor("admin")).SignedString([]byte(pub))

	// HS256 with a guessed secret.
	guessed, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claimsFor("admin")).SignedString([]byte("guessed"))

	// EdDSA with a key the verifier does not know, under a known kid.
	_, stranger, _ := ed25519.GenerateKey(nil)
	unknown := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claimsFor("admin"))
	unknown.Header["kid"] = key.ID
	unknownSigned, _ := unknown.SignedString(stranger)

	// The right key but no kid, or an unknown kid.
	noKid, _ := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claimsFor("admin")).SignedString(key.Key)
	badKid := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claimsFor("admin"))
	badKid.Header["kid"] = "rotated-out"
	badKidSigned, _ := badKid.SignedString(key.Key)

	// The right key and kid, another issuer.
	foreign := claimsFor("admin")
	foreign.Issuer = "someone-else"
	other := jwt.NewWithClaims(jwt.SigningMethodEdDSA, foreign)
	other.Header["kid"] = key.ID
	otherSigned, _ := other.SignedString(key.Key)

	for name, token := range map[string]string{
		"alg none": none, "HS256 keyed with the public key": confused, "HS256 guessed secret": guessed,
		"unknown key": unknownSigned, "no kid": noKid, "unknown kid": badKidSigned, "wrong issuer": otherSigned,
	} {
		if _, err := verifier.Parse(token); err == nil {
			t.Errorf("%s: forged token accepted", name)
		}
	}
}

func TestLegacyHS256OnlyWhileAccepted(t *testing.T) {
	const secret = "former-shared-secret-for-the-rollout"
	legacy, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, authjwt.Claims{UserID: "user-1", Role: "buyer", SessionID: "s",
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}}).SignedString([]byte(secret))

	verifier := authjwttest.Verifier()
	if _, err := verifier.Parse(legacy); err == nil {
		t.Fatal("HS256 accepted without AcceptLegacyHS256")
	}
	verifier.AcceptLegacyHS256(secret, time.Now().Add(time.Hour))
	if claims, err := verifier.Parse(legacy); err != nil || claims.UserID != "user-1" {
		t.Fatalf("legacy token rejected during the rollout: %v", err)
	}
	verifier.AcceptLegacyHS256(secret, time.Now().Add(-time.Second))
	if _, err := verifier.Parse(legacy); err == nil {
		t.Fatal("HS256 still accepted after the rollout window closed")
	}
}

func TestParseVerifyKeysAndSigningKey(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	public := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	b64 := base64.StdEncoding.EncodeToString

	keys, err := authjwt.ParseVerifyKeys("k1:" + b64(public) + ", k2:" + b64(public))
	if err != nil || len(keys) != 2 {
		t.Fatalf("two keys: %v %v", keys, err)
	}
	for _, bad := range []string{"", "k1", "k1:not-base64!", "k1:" + b64([]byte("short")), "bad id:" + b64(public), "k1:" + b64(public) + ",k1:" + b64(public)} {
		if _, err := authjwt.ParseVerifyKeys(bad); err == nil {
			t.Errorf("accepted public keys %q", bad)
		}
	}
	signing, err := authjwt.ParseSigningKey("k1", b64(seed))
	if err != nil || !signing.Key.Public().(ed25519.PublicKey).Equal(public) {
		t.Fatalf("signing key: %v", err)
	}
	for _, bad := range [][2]string{{"", b64(seed)}, {"k1", "CHANGE_ME"}, {"k1", b64(seed[:16])}} {
		if _, err := authjwt.ParseSigningKey(bad[0], bad[1]); err == nil {
			t.Errorf("accepted signing key %q", bad)
		}
	}
	// The published key under the signing kid must be the signing key's.
	_, other, _ := ed25519.GenerateKey(nil)
	if _, err := authjwt.NewIssuer(signing, authjwt.VerifyKeys{"k1": other.Public().(ed25519.PublicKey)}); err == nil ||
		!strings.Contains(err.Error(), "k1") {
		t.Fatalf("conflicting public key accepted: %v", err)
	}
}
