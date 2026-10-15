package bulkimport

import (
	"fmt"
	"net/url"
	"os"
	"strconv"

	"shopee/backend/pkg/platform/objectstorage"
)

// Config is AF-16 in one owner service (Catalog or Inventory).
type Config struct {
	Enabled  bool
	MaxRows  int
	MaxBytes int64
	// Storage is the owner's private bucket for uploads, error reports and
	// exports; nil when IMPORT_STORAGE_ENDPOINT is unset (feature unusable).
	Storage *objectstorage.PrivateConfig
}

// LoadConfig reads the owner's flag, limits and private bucket.
// defaultBucket keeps Catalog and Inventory files apart.
func LoadConfig(defaultBucket string, production bool) (Config, error) {
	c := Config{MaxRows: 1000, MaxBytes: 5 << 20}
	var err error
	if c.Enabled, err = strconv.ParseBool(getEnv("FEATURE_BULK_OPERATIONS_ENABLED", "false")); err != nil {
		return c, fmt.Errorf("config: FEATURE_BULK_OPERATIONS_ENABLED must be true or false")
	}
	if raw := os.Getenv("IMPORT_MAX_ROWS"); raw != "" {
		if c.MaxRows, err = strconv.Atoi(raw); err != nil || c.MaxRows < 1 || c.MaxRows > 1000 {
			return c, fmt.Errorf("config: IMPORT_MAX_ROWS must be 1-1000")
		}
	}
	if raw := os.Getenv("IMPORT_MAX_BYTES"); raw != "" {
		if c.MaxBytes, err = strconv.ParseInt(raw, 10, 64); err != nil || c.MaxBytes < 1024 || c.MaxBytes > 5<<20 {
			return c, fmt.Errorf("config: IMPORT_MAX_BYTES must be 1024-5242880")
		}
	}
	endpoint := os.Getenv("IMPORT_STORAGE_ENDPOINT")
	if endpoint == "" {
		if c.Enabled && production {
			return c, fmt.Errorf("config: FEATURE_BULK_OPERATIONS_ENABLED needs IMPORT_STORAGE_ENDPOINT")
		}
		return c, nil
	}
	s := objectstorage.PrivateConfig{Endpoint: endpoint, Bucket: getEnv("IMPORT_STORAGE_BUCKET", defaultBucket), DownloadEndpoint: os.Getenv("IMPORT_STORAGE_DOWNLOAD_ORIGIN")}
	if s.AccessKey, err = requireEnv("IMPORT_STORAGE_ACCESS_KEY"); err != nil {
		return c, err
	}
	if s.SecretKey, err = requireEnv("IMPORT_STORAGE_SECRET_KEY"); err != nil {
		return c, err
	}
	if s.UseSSL, err = strconv.ParseBool(getEnv("IMPORT_STORAGE_USE_SSL", "false")); err != nil {
		return c, fmt.Errorf("config: IMPORT_STORAGE_USE_SSL must be true or false")
	}
	if s.DownloadEndpoint != "" {
		u, err := url.Parse(s.DownloadEndpoint)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") || (production && u.Scheme != "https") {
			return c, fmt.Errorf("config: IMPORT_STORAGE_DOWNLOAD_ORIGIN must be an origin (HTTPS in production)")
		}
	} else if production && !s.UseSSL {
		return c, fmt.Errorf("config: import downloads require HTTPS in production")
	}
	c.Storage = &s
	return c, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func requireEnv(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("config: required environment variable %q is not set", key)
	}
	return v, nil
}
