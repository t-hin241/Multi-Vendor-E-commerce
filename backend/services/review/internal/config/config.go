package config

import (
	"fmt"
	"os"
	"strconv"

	baseconfig "shopee/backend/pkg/config"
	"shopee/backend/pkg/platform/objectstorage"
)

// Config is Review's configuration; secrets are read here only.
type Config struct {
	Base                                                             baseconfig.Base
	JWTSecret, OrderServiceURL, VendorServiceURL, IdentityServiceURL string
	ObjectStorage                                                    objectstorage.Config
	// ShowUnverified also lists reviews that are not verified purchases
	// (seeded demo data). Refused in production (REV-05).
	ShowUnverified bool
}

func Load() (Config, error) {
	base, err := baseconfig.LoadBase("review", "8091")
	if err != nil {
		return Config{}, err
	}
	jwt, err := baseconfig.RequireJWTSecret()
	if err != nil {
		return Config{}, err
	}
	orderURL, err := requireEnv("ORDER_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}
	vendorURL, err := requireEnv("VENDOR_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}
	identityURL, err := requireEnv("IDENTITY_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}
	endpoint, err := requireEnv("OBJECT_STORAGE_ENDPOINT")
	if err != nil {
		return Config{}, err
	}
	access, err := requireEnv("OBJECT_STORAGE_ACCESS_KEY")
	if err != nil {
		return Config{}, err
	}
	secret, err := requireEnv("OBJECT_STORAGE_SECRET_KEY")
	if err != nil {
		return Config{}, err
	}
	bucket, err := requireEnv("OBJECT_STORAGE_BUCKET")
	if err != nil {
		return Config{}, err
	}
	publicURL, err := requireEnv("OBJECT_STORAGE_PUBLIC_BASE_URL")
	if err != nil {
		return Config{}, err
	}
	ssl, _ := strconv.ParseBool(os.Getenv("OBJECT_STORAGE_USE_SSL"))
	showUnverified := os.Getenv("REVIEW_SHOW_UNVERIFIED") == "true"
	if showUnverified && base.Env == "production" {
		return Config{}, fmt.Errorf("config: REVIEW_SHOW_UNVERIFIED is not allowed in production")
	}
	return Config{ShowUnverified: showUnverified, Base: base, JWTSecret: jwt, OrderServiceURL: orderURL, VendorServiceURL: vendorURL, IdentityServiceURL: identityURL, ObjectStorage: objectstorage.Config{Endpoint: endpoint, AccessKey: access, SecretKey: secret, Bucket: bucket, PublicBaseURL: publicURL, UseSSL: ssl}}, nil
}
func requireEnv(key string) (string, error) {
	if v := os.Getenv(key); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("config: required environment variable %q is not set", key)
}
