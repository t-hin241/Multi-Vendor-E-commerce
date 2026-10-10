// Package config extends the shared base config with the settings unique to
// Inventory: where to reach Vendor and Catalog for ownership checks.
package config

import (
	"fmt"
	"os"
	"strconv"

	"shopee/backend/pkg/config"
)

type Config struct {
	OrderServiceURL string
	ExpiryEnabled   bool
	// SecondApprovalQuantity (PW-027): restock requests of at least this
	// many units need two admins; 0 (default) turns it off.
	SecondApprovalQuantity int64
	Base                   config.Base
	VendorServiceURL       string
	CatalogServiceURL      string
}

func Load() (Config, error) {
	base, err := config.LoadBase("inventory", "8084")
	if err != nil {
		return Config{}, err
	}

	vendorServiceURL, err := requireEnv("VENDOR_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}

	catalogServiceURL, err := requireEnv("CATALOG_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}

	orderURL := os.Getenv("ORDER_SERVICE_URL")
	if orderURL == "" {
		orderURL = "http://order:8086"
	}
	expiry := false
	if raw := os.Getenv("INVENTORY_EXPIRY_ENABLED"); raw != "" {
		var e error
		expiry, e = strconv.ParseBool(raw)
		if e != nil {
			return Config{}, fmt.Errorf("INVENTORY_EXPIRY_ENABLED must be a boolean")
		}
	}
	var secondApproval int64
	if raw := os.Getenv("INVENTORY_RESTOCK_SECOND_APPROVAL_QUANTITY"); raw != "" {
		v, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || v < 0 {
			return Config{}, fmt.Errorf("INVENTORY_RESTOCK_SECOND_APPROVAL_QUANTITY must be a non-negative integer")
		}
		secondApproval = v
	}
	return Config{OrderServiceURL: orderURL, ExpiryEnabled: expiry, SecondApprovalQuantity: secondApproval,
		Base:              base,
		VendorServiceURL:  vendorServiceURL,
		CatalogServiceURL: catalogServiceURL,
	}, nil
}

func requireEnv(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", fmt.Errorf("config: required environment variable %q is not set", key)
	}
	return v, nil
}
