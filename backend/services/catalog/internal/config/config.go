// Package config extends the shared base config with the settings unique to
// Catalog: where to reach Vendor for approval checks and shop names, where
// to reach Order for storefront sold-counts, and how to reach the object
// store for product images.
package config

import (
	"fmt"
	"os"
	"strconv"

	"shopee/backend/pkg/config"
	"shopee/backend/pkg/platform/objectstorage"
)

type Config struct {
	Base                config.Base
	JWTSecret           string
	VendorServiceURL    string
	OrderServiceURL     string
	InventoryServiceURL string
	ObjectStorage       objectstorage.Config
}

func Load() (Config, error) {
	base, err := config.LoadBase("catalog", "8083")
	if err != nil {
		return Config{}, err
	}

	jwtSecret, err := config.RequireJWTSecret()
	if err != nil {
		return Config{}, err
	}

	vendorServiceURL, err := requireEnv("VENDOR_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}

	orderServiceURL, err := requireEnv("ORDER_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}

	inventoryServiceURL, err := requireEnv("INVENTORY_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}

	objectStorageCfg, err := loadObjectStorageConfig()
	if err != nil {
		return Config{}, err
	}

	return Config{
		Base:                base,
		JWTSecret:           jwtSecret,
		VendorServiceURL:    vendorServiceURL,
		OrderServiceURL:     orderServiceURL,
		InventoryServiceURL: inventoryServiceURL,
		ObjectStorage:       objectStorageCfg,
	}, nil
}

func loadObjectStorageConfig() (objectstorage.Config, error) {
	endpoint, err := requireEnv("OBJECT_STORAGE_ENDPOINT")
	if err != nil {
		return objectstorage.Config{}, err
	}
	accessKey, err := requireEnv("OBJECT_STORAGE_ACCESS_KEY")
	if err != nil {
		return objectstorage.Config{}, err
	}
	secretKey, err := requireEnv("OBJECT_STORAGE_SECRET_KEY")
	if err != nil {
		return objectstorage.Config{}, err
	}
	bucket, err := requireEnv("OBJECT_STORAGE_BUCKET")
	if err != nil {
		return objectstorage.Config{}, err
	}
	publicBaseURL, err := requireEnv("OBJECT_STORAGE_PUBLIC_BASE_URL")
	if err != nil {
		return objectstorage.Config{}, err
	}

	useSSL, _ := strconv.ParseBool(os.Getenv("OBJECT_STORAGE_USE_SSL"))

	return objectstorage.Config{
		Endpoint:      endpoint,
		AccessKey:     accessKey,
		SecretKey:     secretKey,
		UseSSL:        useSSL,
		Bucket:        bucket,
		PublicBaseURL: publicBaseURL,
	}, nil
}

func requireEnv(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", fmt.Errorf("config: required environment variable %q is not set", key)
	}
	return v, nil
}
