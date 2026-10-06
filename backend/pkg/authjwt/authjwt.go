// Package authjwt issues and verifies the access tokens used across every
// service. Tokens are signed with Ed25519 (EdDSA): only Identity holds the
// private key and can issue them; every other service holds public keys
// only, so a service (or a leak of its environment) can verify a user's
// token but never mint one, e.g. with an admin role.
package authjwt

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Issuer is the iss claim of every token Identity signs.
const Issuer = "shopee-identity"

// Claims is the access token payload: which user, and which role they had
// at the time the token was issued.
type Claims struct {
	SessionID string `json:"sid,omitempty"`
	UserID    string `json:"sub"`
	Role      string `json:"role"`
	jwt.RegisteredClaims
}

// VerifyKeys are the public keys tokens may be signed with, by key id (the
// kid header). Several at once allow a rotation: the new key is published
// to every verifier before Identity signs with it.
type VerifyKeys map[string]ed25519.PublicKey

// SigningKey is Identity's private key and its key id.
type SigningKey struct {
	ID  string
	Key ed25519.PrivateKey
}

var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

// ParseVerifyKeys reads "kid:base64(32-byte public key)[,kid:...]"
// (JWT_PUBLIC_KEYS, deploy/gen-jwt-keys.sh).
func ParseVerifyKeys(s string) (VerifyKeys, error) {
	keys := VerifyKeys{}
	for _, entry := range strings.Split(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		id, encoded, ok := strings.Cut(entry, ":")
		if !ok || !keyIDPattern.MatchString(id) {
			return nil, fmt.Errorf("authjwt: public key entries are kid:base64")
		}
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("authjwt: public key %q is not a base64 Ed25519 public key", id)
		}
		if _, dup := keys[id]; dup {
			return nil, fmt.Errorf("authjwt: public key id %q appears twice", id)
		}
		keys[id] = ed25519.PublicKey(raw)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("authjwt: no public key")
	}
	return keys, nil
}

// ParseSigningKey reads a key id and a base64 Ed25519 seed (32 bytes)
// (JWT_SIGNING_KEY_ID, JWT_SIGNING_KEY).
func ParseSigningKey(id, encodedSeed string) (SigningKey, error) {
	if !keyIDPattern.MatchString(id) {
		return SigningKey{}, fmt.Errorf("authjwt: invalid signing key id")
	}
	seed, err := base64.StdEncoding.DecodeString(encodedSeed)
	if err != nil || len(seed) != ed25519.SeedSize {
		return SigningKey{}, fmt.Errorf("authjwt: signing key is not a base64 Ed25519 seed (32 bytes)")
	}
	return SigningKey{ID: id, Key: ed25519.NewKeyFromSeed(seed)}, nil
}

// Manager verifies access tokens and, for Identity only, issues them.
type Manager struct {
	signing *SigningKey
	keys    VerifyKeys
	// legacy is the former shared HS256 secret, accepted (never used to
	// sign) only until legacyUntil, while a deployment switches over.
	legacy      []byte
	legacyUntil time.Time
	verify      func(context.Context, *Claims) error
}

// NewVerifier verifies tokens against keys and cannot issue any.
func NewVerifier(keys VerifyKeys) *Manager {
	return &Manager{keys: keys}
}

// NewIssuer signs with signing and verifies against keys plus signing's own
// public key. Only Identity creates one.
func NewIssuer(signing SigningKey, keys VerifyKeys) (*Manager, error) {
	all := VerifyKeys{}
	for id, k := range keys {
		all[id] = k
	}
	public := signing.Key.Public().(ed25519.PublicKey)
	if held, ok := all[signing.ID]; ok && !held.Equal(public) {
		return nil, fmt.Errorf("authjwt: JWT_PUBLIC_KEYS has another key under the signing key id %q", signing.ID)
	}
	all[signing.ID] = public
	return &Manager{signing: &signing, keys: all}, nil
}

// AcceptLegacyHS256 also accepts tokens signed with the former shared
// secret until until: the minutes of a rollout during which tokens issued
// before the switch are still alive (access tokens live 15 minutes).
func (m *Manager) AcceptLegacyHS256(secret string, until time.Time) {
	m.legacy, m.legacyUntil = []byte(secret), until
}

func (m *Manager) legacyOpen() bool { return len(m.legacy) > 0 && time.Now().Before(m.legacyUntil) }

func (m *Manager) SetVerifier(verify func(context.Context, *Claims) error) { m.verify = verify }
func (m *Manager) VerifySession(ctx context.Context, claims *Claims) error {
	if m.verify != nil {
		return m.verify(ctx, claims)
	}
	return nil
}

// ErrCannotIssue: this manager holds no private key (every service but Identity).
var ErrCannotIssue = errors.New("authjwt: this service cannot issue tokens")

// IssueAccessToken signs a short-lived access token for userID/role.
func (m *Manager) IssueAccessToken(userID, role string, ttl time.Duration) (string, time.Time, error) {
	return m.IssueSessionToken(userID, role, "", ttl)
}
func (m *Manager) IssueSessionToken(userID, role, sessionID string, ttl time.Duration) (string, time.Time, error) {
	if m.signing == nil {
		return "", time.Time{}, ErrCannotIssue
	}
	now := time.Now()
	expiresAt := now.Add(ttl)
	claims := Claims{
		SessionID: sessionID,
		UserID:    userID,
		Role:      role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    Issuer,
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = m.signing.ID
	signed, err := token.SignedString(m.signing.Key)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expiresAt, nil
}

var ErrInvalidToken = errors.New("authjwt: invalid or expired token")
var ErrVerificationUnavailable = errors.New("authjwt: session verification unavailable")

// Parse verifies the token's signature (by its kid), issuer and expiry and
// returns its claims. Only EdDSA is accepted (plus HS256 while
// AcceptLegacyHS256 is on): a token naming another algorithm, no key id or
// an unknown one is rejected.
func (m *Manager) Parse(tokenString string) (*Claims, error) {
	methods := []string{jwt.SigningMethodEdDSA.Alg()}
	if m.legacyOpen() {
		methods = append(methods, jwt.SigningMethodHS256.Alg())
	}
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		switch t.Method.(type) {
		case *jwt.SigningMethodEd25519:
			kid, _ := t.Header["kid"].(string)
			key, ok := m.keys[kid]
			if !ok {
				return nil, ErrInvalidToken
			}
			return key, nil
		case *jwt.SigningMethodHMAC:
			if !m.legacyOpen() {
				return nil, ErrInvalidToken
			}
			return m.legacy, nil
		}
		return nil, ErrInvalidToken
	}, jwt.WithValidMethods(methods), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	// Legacy tokens predate the issuer claim; every EdDSA token carries it.
	if _, eddsa := token.Method.(*jwt.SigningMethodEd25519); eddsa && claims.Issuer != Issuer {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
