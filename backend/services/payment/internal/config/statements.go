package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	"shopee/backend/pkg/platform/objectstorage"
)

type StatementConfig struct {
	Enabled   bool
	Retention time.Duration
	Storage   *objectstorage.PrivateConfig
}

func loadStatements(production bool) (StatementConfig, error) {
	var c StatementConfig
	var err error
	c.Enabled, err = strconv.ParseBool(getEnv("FEATURE_VENDOR_STATEMENTS_ENABLED", "false"))
	if err != nil {
		return c, fmt.Errorf("config: FEATURE_VENDOR_STATEMENTS_ENABLED must be true or false")
	}
	hours, err := strconv.Atoi(getEnv("EXPORT_RETENTION_HOURS", "24"))
	if err != nil || hours < 1 || hours > 168 {
		return c, fmt.Errorf("config: EXPORT_RETENTION_HOURS must be 1-168")
	}
	c.Retention = time.Duration(hours) * time.Hour
	endpoint := os.Getenv("STATEMENT_STORAGE_ENDPOINT")
	if endpoint == "" {
		return c, nil
	}
	s := objectstorage.PrivateConfig{Endpoint: endpoint, Bucket: getEnv("STATEMENT_STORAGE_BUCKET", "vendor-statements"), DownloadEndpoint: os.Getenv("STATEMENT_STORAGE_DOWNLOAD_ORIGIN")}
	if s.AccessKey, err = requireEnv("STATEMENT_STORAGE_ACCESS_KEY"); err != nil {
		return c, err
	}
	if s.SecretKey, err = requireEnv("STATEMENT_STORAGE_SECRET_KEY"); err != nil {
		return c, err
	}
	if s.UseSSL, err = strconv.ParseBool(getEnv("STATEMENT_STORAGE_USE_SSL", "false")); err != nil {
		return c, fmt.Errorf("config: STATEMENT_STORAGE_USE_SSL must be true or false")
	}
	if s.DownloadEndpoint != "" {
		u, err := url.Parse(s.DownloadEndpoint)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") || (production && u.Scheme != "https") {
			return c, fmt.Errorf("config: STATEMENT_STORAGE_DOWNLOAD_ORIGIN must be an origin (HTTPS in production)")
		}
	} else if production && !s.UseSSL {
		return c, fmt.Errorf("config: statement downloads require HTTPS in production")
	}
	c.Storage = &s
	return c, nil
}
