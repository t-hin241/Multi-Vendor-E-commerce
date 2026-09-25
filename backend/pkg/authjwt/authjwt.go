// Package authjwt issues and verifies the access tokens used across every
// service. Only Identity issues tokens; every other service that checks
// auth verifies with the same shared secret, so no service has to trust a
// user id or role handed to it by the client or the gateway.
package authjwt

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the access token payload: which user, and which role they had
// at the time the token was issued.
type Claims struct {
	UserID string `json:"sub"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// Manager issues and verifies HS256 access tokens from a shared secret.
type Manager struct {
	secret []byte
}

func NewManager(secret string) *Manager {
	return &Manager{secret: []byte(secret)}
}

// IssueAccessToken signs a short-lived access token for userID/role.
func (m *Manager) IssueAccessToken(userID, role string, ttl time.Duration) (string, time.Time, error) {
	expiresAt := time.Now().Add(ttl)
	claims := Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expiresAt, nil
}

var ErrInvalidToken = errors.New("authjwt: invalid or expired token")

// Parse verifies the token's signature and expiry and returns its claims.
func (m *Manager) Parse(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return m.secret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
