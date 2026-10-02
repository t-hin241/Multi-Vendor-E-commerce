package config

import (
	"fmt"
	"os"

	"shopee/backend/pkg/serviceauth"
)

// InternalServices is how a service calls others and checks who calls it.
type InternalServices struct {
	// Key is this service's credential for outgoing internal calls
	// (serviceauth.Credential), or the legacy shared key.
	Key         string
	IdentityURL string
	// Verifier checks incoming internal calls.
	Verifier *serviceauth.Verifier
}

// LoadInternalServices reads the service identity (PLT-01):
//
//	INTERNAL_SERVICE_NAME, INTERNAL_SERVICE_KEY  this service's name and secret key
//	INTERNAL_SERVICE_KEYS                        name=sha256(key) of every service (not secret)
//	IDENTITY_SERVICE_KEY                         former shared key
//	INTERNAL_AUTH_ACCEPT_SHARED_KEY=true         also accept the shared key (rollout only)
//
// Without INTERNAL_SERVICE_KEY the service runs in legacy mode (shared key
// both ways), refused in production unless the rollout flag is set.
func LoadInternalServices() (InternalServices, error) {
	identityURL := getEnv("IDENTITY_SERVICE_URL", "http://identity:8081")
	acceptShared := os.Getenv("INTERNAL_AUTH_ACCEPT_SHARED_KEY") == "true"
	shared := os.Getenv("IDENTITY_SERVICE_KEY")
	if shared != "" && (len(shared) < 32 || shared == "CHANGE_ME") {
		return InternalServices{}, fmt.Errorf("IDENTITY_SERVICE_KEY must contain at least 32 characters")
	}
	production := getEnv("ENV", "development") == "production"

	name, key := os.Getenv("INTERNAL_SERVICE_NAME"), os.Getenv("INTERNAL_SERVICE_KEY")
	if key == "CHANGE_ME" { // the .env.example placeholder: not configured
		key = ""
	}
	if key == "" {
		if shared == "" {
			return InternalServices{}, fmt.Errorf("config: INTERNAL_SERVICE_KEY (or IDENTITY_SERVICE_KEY in legacy mode) is required")
		}
		if production && !acceptShared {
			return InternalServices{}, fmt.Errorf("config: production requires INTERNAL_SERVICE_NAME/INTERNAL_SERVICE_KEY (per-service keys)")
		}
		return InternalServices{Key: shared, IdentityURL: identityURL, Verifier: serviceauth.SharedKey(shared)}, nil
	}
	if len(key) < 32 {
		return InternalServices{}, fmt.Errorf("config: INTERNAL_SERVICE_KEY must contain at least 32 characters")
	}
	registry, err := serviceauth.ParseRegistry(os.Getenv("INTERNAL_SERVICE_KEYS"))
	if err != nil {
		return InternalServices{}, err
	}
	if len(registry[name]) == 0 {
		return InternalServices{}, fmt.Errorf("config: INTERNAL_SERVICE_NAME %q has no key hash in INTERNAL_SERVICE_KEYS", name)
	}
	if !contains(registry[name], serviceauth.HashKey(key)) {
		return InternalServices{}, fmt.Errorf("config: INTERNAL_SERVICE_KEY does not match the hash registered for %q", name)
	}
	accepted := ""
	if acceptShared {
		accepted = shared
	}
	return InternalServices{Key: serviceauth.Credential(name, key), IdentityURL: identityURL,
		Verifier: serviceauth.NewVerifier(registry, accepted)}, nil
}

func contains(hashes [][]byte, hexHash string) bool {
	for _, h := range hashes {
		if fmt.Sprintf("%x", h) == hexHash {
			return true
		}
	}
	return false
}
