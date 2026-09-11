// Package config extends the shared base config with the settings unique to
// Order: where to reach Cart, Catalog, Vendor and Inventory for the
// checkout saga.
package config

import (
	"fmt"
	"os"

	"shopee/backend/pkg/config"
)

type Config struct {
	Base                   config.Base
	JWTSecret              string
	CartServiceURL         string
	CatalogServiceURL      string
	VendorServiceURL       string
	InventoryServiceURL    string
	ShipmentServiceURL     string
	NotificationServiceURL string
}

func Load() (Config, error) {
	base, err := config.LoadBase("order", "8086")
	if err != nil {
		return Config{}, err
	}

	jwtSecret, err := config.RequireJWTSecret()
	if err != nil {
		return Config{}, err
	}

	cartServiceURL, err := requireEnv("CART_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}
	catalogServiceURL, err := requireEnv("CATALOG_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}
	vendorServiceURL, err := requireEnv("VENDOR_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}
	inventoryServiceURL, err := requireEnv("INVENTORY_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}
	shipmentServiceURL, err := requireEnv("SHIPMENT_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}
	notificationServiceURL, err := requireEnv("NOTIFICATION_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}

	return Config{
		Base:                   base,
		JWTSecret:              jwtSecret,
		CartServiceURL:         cartServiceURL,
		CatalogServiceURL:      catalogServiceURL,
		VendorServiceURL:       vendorServiceURL,
		InventoryServiceURL:    inventoryServiceURL,
		ShipmentServiceURL:     shipmentServiceURL,
		NotificationServiceURL: notificationServiceURL,
	}, nil
}

func requireEnv(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", fmt.Errorf("config: required environment variable %q is not set", key)
	}
	return v, nil
}
