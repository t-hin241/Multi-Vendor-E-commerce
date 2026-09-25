// Package config extends the shared base config with the settings unique to
// Vendor: the JWT secret, where to reach Notification for approval
// decisions, and how to reach the object store for shop logo/banner images.
package config

import (
	"fmt"
	"os"
	"strconv"

	"shopee/backend/pkg/config"
	"shopee/backend/pkg/platform/objectstorage"
)

type Config struct {
	Base                   config.Base
	JWTSecret              string
	NotificationServiceURL string
	ObjectStorage          objectstorage.Config
}

func Load() (Config, error) {
	base, err := config.LoadBase("vendor", "8082")
	if err != nil {
		return Config{}, err
	}

	jwtSecret, err := config.RequireJWTSecret()
	if err != nil {
		return Config{}, err
	}

	notificationServiceURL, err := requireEnv("NOTIFICATION_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}

	objectStorageCfg, err := loadObjectStorageConfig()
	if err != nil {
		return Config{}, err
	}

	return Config{
		Base:                   base,
		JWTSecret:              jwtSecret,
		NotificationServiceURL: notificationServiceURL,
		ObjectStorage:          objectStorageCfg,
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
