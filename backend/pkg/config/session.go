package config

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"shopee/backend/pkg/authjwt"
)

func LoadSessionVerifier() (func(context.Context, *authjwt.Claims) error, error) {
	key, err := requireEnv("IDENTITY_SERVICE_KEY")
	if err != nil {
		return nil, err
	}
	if len(key) < 32 || key == "CHANGE_ME" {
		return nil, fmt.Errorf("config: IDENTITY_SERVICE_KEY must contain at least 32 characters")
	}
	base, err := requireEnv("IDENTITY_SERVICE_URL")
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, fmt.Errorf("config: invalid IDENTITY_SERVICE_URL")
	}
	return authjwt.RemoteVerifier(strings.TrimRight(base, "/"), key), nil
}
