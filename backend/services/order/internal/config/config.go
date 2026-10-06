// Package config extends the shared base config with the settings unique to
// Order: where to reach Cart, Catalog, Vendor and Inventory for the
// checkout saga.
package config

import (
	"fmt"
	"os"
	"strconv"

	"shopee/backend/pkg/config"
)

type Config struct {
	Base                   config.Base
	CartServiceURL         string
	CatalogServiceURL      string
	VendorServiceURL       string
	InventoryServiceURL    string
	ShipmentServiceURL     string
	NotificationServiceURL string
	PaymentServiceURL      string
	// ReturnWindowDays is how long after delivery (completion) an item may
	// be returned; it is copied onto every return request with its version.
	ReturnWindowDays int
}

// ReturnPolicyVersion names the policy in force, derived from its settings
// so a changed window is always a new version.
func (c Config) ReturnPolicyVersion() string {
	return "window-" + strconv.Itoa(c.ReturnWindowDays) + "d"
}

func Load() (Config, error) {
	base, err := config.LoadBase("order", "8086")
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
	paymentServiceURL, err := requireEnv("PAYMENT_SERVICE_URL")
	if err != nil {
		return Config{}, err
	}
	returnWindowDays := 7
	if raw := os.Getenv("ORDER_RETURN_WINDOW_DAYS"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 365 {
			return Config{}, fmt.Errorf("config: ORDER_RETURN_WINDOW_DAYS must be an integer between 1 and 365")
		}
		returnWindowDays = v
	}

	return Config{
		Base:                   base,
		CartServiceURL:         cartServiceURL,
		CatalogServiceURL:      catalogServiceURL,
		VendorServiceURL:       vendorServiceURL,
		InventoryServiceURL:    inventoryServiceURL,
		ShipmentServiceURL:     shipmentServiceURL,
		NotificationServiceURL: notificationServiceURL,
		PaymentServiceURL:      paymentServiceURL,
		ReturnWindowDays:       returnWindowDays,
	}, nil
}

func requireEnv(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", fmt.Errorf("config: required environment variable %q is not set", key)
	}
	return v, nil
}
