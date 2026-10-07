// Package config extends the shared base config with the settings unique to
// Order: where to reach Cart, Catalog, Vendor and Inventory for the
// checkout saga.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"shopee/backend/pkg/config"
	"shopee/backend/pkg/platform/objectstorage"
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
	Support          SupportConfig
	// VersionedPolicies is FEATURE_VERSIONED_POLICIES_ENABLED (AF-02): new
	// orders snapshot the published policies in force.
	VersionedPolicies bool
}

// SupportConfig is the rollout of order support cases (AF-01).
type SupportConfig struct {
	// Enabled accepts new cases; off keeps existing cases readable,
	// answerable and resolvable.
	Enabled bool
	// PilotVendorIDs, when set, limits new cases to these shops.
	PilotVendorIDs []string
	// AttachmentRetentionDays is how long evidence stays after a case closes.
	AttachmentRetentionDays int
	// Attachments is the private evidence bucket; nil turns attachments off.
	Attachments *objectstorage.PrivateConfig
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

	support, err := loadSupportConfig()
	if err != nil {
		return Config{}, err
	}
	versionedPolicies := false
	if raw := os.Getenv("FEATURE_VERSIONED_POLICIES_ENABLED"); raw != "" {
		if versionedPolicies, err = strconv.ParseBool(raw); err != nil {
			return Config{}, fmt.Errorf("config: FEATURE_VERSIONED_POLICIES_ENABLED must be true or false")
		}
	}

	return Config{
		Support:                support,
		VersionedPolicies:      versionedPolicies,
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

func loadSupportConfig() (SupportConfig, error) {
	cfg := SupportConfig{AttachmentRetentionDays: 180}
	if raw := os.Getenv("FEATURE_ORDER_SUPPORT_ENABLED"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return SupportConfig{}, fmt.Errorf("config: FEATURE_ORDER_SUPPORT_ENABLED must be true or false")
		}
		cfg.Enabled = v
	}
	for _, id := range strings.Split(os.Getenv("FEATURE_ORDER_SUPPORT_PILOT_VENDOR_IDS"), ",") {
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		if _, err := uuid.Parse(id); err != nil {
			return SupportConfig{}, fmt.Errorf("config: FEATURE_ORDER_SUPPORT_PILOT_VENDOR_IDS must be a comma-separated list of vendor ids")
		}
		cfg.PilotVendorIDs = append(cfg.PilotVendorIDs, id)
	}
	if raw := os.Getenv("SUPPORT_ATTACHMENT_RETENTION_DAYS"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 3650 {
			return SupportConfig{}, fmt.Errorf("config: SUPPORT_ATTACHMENT_RETENTION_DAYS must be an integer between 1 and 3650")
		}
		cfg.AttachmentRetentionDays = v
	}
	// Attachments are optional: without a private bucket, cases work
	// without evidence images.
	endpoint := os.Getenv("SUPPORT_ATTACHMENT_STORAGE_ENDPOINT")
	if endpoint == "" {
		return cfg, nil
	}
	private := objectstorage.PrivateConfig{Endpoint: endpoint}
	var err error
	if private.AccessKey, err = requireEnv("SUPPORT_ATTACHMENT_STORAGE_ACCESS_KEY"); err != nil {
		return SupportConfig{}, err
	}
	if private.SecretKey, err = requireEnv("SUPPORT_ATTACHMENT_STORAGE_SECRET_KEY"); err != nil {
		return SupportConfig{}, err
	}
	if private.Bucket, err = requireEnv("SUPPORT_ATTACHMENT_BUCKET"); err != nil {
		return SupportConfig{}, err
	}
	private.UseSSL, _ = strconv.ParseBool(os.Getenv("SUPPORT_ATTACHMENT_STORAGE_USE_SSL"))
	cfg.Attachments = &private
	return cfg, nil
}

func requireEnv(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", fmt.Errorf("config: required environment variable %q is not set", key)
	}
	return v, nil
}
