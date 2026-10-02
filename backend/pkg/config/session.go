package config

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/serviceauth"
)

// LoadSessionVerifier checks every access token's session with Identity,
// identifying this service with its own credential (LoadInternalServices).
func LoadSessionVerifier() (func(context.Context, *authjwt.Claims) error, error) {
	internal, err := LoadInternalServices()
	if err != nil {
		return nil, err
	}
	base, err := requireEnv("IDENTITY_SERVICE_URL")
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, fmt.Errorf("config: invalid IDENTITY_SERVICE_URL")
	}
	credential := internal.Key
	return authjwt.RemoteVerifierWith(strings.TrimRight(base, "/"), func(req *http.Request) {
		serviceauth.SetRequestHeaders(req, credential)
	}), nil
}
