// Package serviceauth authenticates calls between services (PLT-01).
//
// Every service has its own name and secret key and sends both on each
// internal call. A receiver knows only the SHA-256 hashes of the other
// services' keys (INTERNAL_SERVICE_KEYS, not secret), so no service can
// impersonate another, and every internal route names the services allowed
// to call it (least privilege). The former shared key is accepted only
// during a rollout window (INTERNAL_AUTH_ACCEPT_SHARED_KEY=true); it
// carries no caller identity, so it passes every route.
package serviceauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
)

const (
	// Header carries the caller's key (name kept for compatibility).
	Header = "X-Identity-Service-Key"
	// CallerHeader carries the caller's service name.
	CallerHeader = "X-Service-Name"
	// credentialPrefix marks a named credential ("svc:<name>:<key>").
	credentialPrefix = "svc:"
	// MaxBody bounds an internal request body.
	MaxBody = 64 << 10
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// Credential encodes this service's name and key as the opaque string the
// HTTP clients carry ("svc:<name>:<key>").
func Credential(name, key string) string { return credentialPrefix + name + ":" + key }

// SetRequestHeaders adds the caller's identity and the request id to an
// outgoing internal call. credential is Credential(...) or, in legacy
// mode, the shared key.
func SetRequestHeaders(req *http.Request, credential string) {
	if name, key, ok := split(credential); ok {
		req.Header.Set(CallerHeader, name)
		req.Header.Set(Header, key)
	} else {
		req.Header.Set(Header, credential)
	}
	if id := middleware.RequestIDFromContext(req.Context()); id != "" {
		req.Header.Set(middleware.RequestIDHeader, id)
	}
}

func split(credential string) (name, key string, ok bool) {
	rest, found := strings.CutPrefix(credential, credentialPrefix)
	if !found {
		return "", "", false
	}
	name, key, ok = strings.Cut(rest, ":")
	return name, key, ok && namePattern.MatchString(name) && key != ""
}

// Verifier knows which key hashes belong to which service.
type Verifier struct {
	hashes map[string][][]byte
	shared []byte // legacy shared key; nil when not accepted
}

// ParseRegistry reads "name=sha256hex|sha256hex,name2=..." (several hashes
// per service during a key rotation).
func ParseRegistry(registry string) (map[string][][]byte, error) {
	out := map[string][][]byte{}
	for _, entry := range strings.Split(registry, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, hashes, ok := strings.Cut(entry, "=")
		if !ok || !namePattern.MatchString(name) {
			return nil, fmt.Errorf("serviceauth: invalid registry entry for %q", name)
		}
		for _, h := range strings.Split(hashes, "|") {
			sum, err := hex.DecodeString(strings.TrimSpace(h))
			if err != nil || len(sum) != sha256.Size {
				return nil, fmt.Errorf("serviceauth: invalid key hash for %q", name)
			}
			out[name] = append(out[name], sum)
		}
	}
	return out, nil
}

// NewVerifier builds a verifier; sharedKey is accepted from any caller
// only when non-empty (rollout window).
func NewVerifier(registry map[string][][]byte, sharedKey string) *Verifier {
	v := &Verifier{hashes: registry}
	if sharedKey != "" {
		v.shared = []byte(sharedKey)
	}
	return v
}

// HashKey is the registry form of a key.
func HashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Authenticate returns the verified caller ("" for the legacy shared key)
// and whether the call is authentic.
func (v *Verifier) Authenticate(name, key string) (string, bool) {
	if v == nil || key == "" {
		return "", false
	}
	if name != "" {
		sum := sha256.Sum256([]byte(key))
		match := 0
		for _, h := range v.hashes[name] {
			match |= subtle.ConstantTimeCompare(sum[:], h)
		}
		return name, match == 1
	}
	if v.shared != nil && subtle.ConstantTimeCompare(v.shared, []byte(key)) == 1 {
		return "", true
	}
	return "", false
}

// Allow is the middleware of one internal route: the caller must be
// authentic and one of callers (any registered service when none given).
// The verified caller is set as "service_caller" for logs.
func (v *Verifier) Allow(callers ...string) gin.HandlerFunc {
	allowed := map[string]bool{}
	for _, c := range callers {
		allowed[c] = true
	}
	return func(c *gin.Context) {
		caller, ok := v.Authenticate(c.GetHeader(CallerHeader), c.GetHeader(Header))
		if !ok {
			httpresponse.Error(c, http.StatusForbidden, "forbidden", "Service authentication required")
			c.Abort()
			return
		}
		if caller != "" && len(allowed) > 0 && !allowed[caller] {
			httpresponse.Error(c, http.StatusForbidden, "forbidden", "This service may not call this route")
			c.Abort()
			return
		}
		if caller == "" {
			caller = "shared-key"
		}
		c.Set("service_caller", caller)
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxBody)
		c.Next()
	}
}

// Require protects a route with a single-purpose key in its own header
// (password-reset delivery, payout details), not a service identity.
func Require(key, header string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(c.GetHeader(header))) != 1 {
			httpresponse.Error(c, http.StatusForbidden, "forbidden", "Service authentication required")
			c.Abort()
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxBody)
		c.Next()
	}
}

// SharedKey is a verifier accepting only one shared key (tests, legacy).
func SharedKey(key string) *Verifier { return NewVerifier(nil, key) }
