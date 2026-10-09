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
	// PaidCancellation is FEATURE_PAID_CANCELLATION_ENABLED (AF-03): accept
	// new cancellation requests on paid packages; off keeps open ones going.
	PaidCancellation bool
	// DeliveryRedelivery is FEATURE_DELIVERY_RESOLUTION_ENABLED (AF-04):
	// offer redeliveries of failed packages. Delivery exception cases are
	// always recorded and resolvable (refund) once Shipment reports them.
	DeliveryRedelivery bool
	// ReturnShipping is FEATURE_RETURN_SHIPPING_ENABLED (AF-05): approved
	// returns get shipping instructions and a return parcel; returns
	// already authorized keep going when it is off. ReturnDispatchDays
	// (ORDER_RETURN_DISPATCH_DAYS, 1-30, default 7) is the deadline.
	ReturnShipping     bool
	ReturnDispatchDays int
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
	// HoldLedger is FEATURE_SETTLEMENT_HOLD_LEDGER_ENABLED (PW-001): cases
	// that may affect money acquire a hold in Payment's ledger. Off still
	// releases holds acquired earlier.
	HoldLedger bool
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
	paidCancellation := false
	if raw := os.Getenv("FEATURE_PAID_CANCELLATION_ENABLED"); raw != "" {
		if paidCancellation, err = strconv.ParseBool(raw); err != nil {
			return Config{}, fmt.Errorf("config: FEATURE_PAID_CANCELLATION_ENABLED must be true or false")
		}
	}
	deliveryRedelivery := false
	if raw := os.Getenv("FEATURE_DELIVERY_RESOLUTION_ENABLED"); raw != "" {
		if deliveryRedelivery, err = strconv.ParseBool(raw); err != nil {
			return Config{}, fmt.Errorf("config: FEATURE_DELIVERY_RESOLUTION_ENABLED must be true or false")
		}
	}
	returnShipping := false
	if raw := os.Getenv("FEATURE_RETURN_SHIPPING_ENABLED"); raw != "" {
		if returnShipping, err = strconv.ParseBool(raw); err != nil {
			return Config{}, fmt.Errorf("config: FEATURE_RETURN_SHIPPING_ENABLED must be true or false")
		}
	}
	returnDispatchDays := 7
	if raw := os.Getenv("ORDER_RETURN_DISPATCH_DAYS"); raw != "" {
		if returnDispatchDays, err = strconv.Atoi(raw); err != nil || returnDispatchDays < 1 || returnDispatchDays > 30 {
			return Config{}, fmt.Errorf("config: ORDER_RETURN_DISPATCH_DAYS must be between 1 and 30")
		}
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
		PaidCancellation:       paidCancellation,
		DeliveryRedelivery:     deliveryRedelivery,
		ReturnShipping:         returnShipping,
		ReturnDispatchDays:     returnDispatchDays,
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
	if raw := os.Getenv("FEATURE_SETTLEMENT_HOLD_LEDGER_ENABLED"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return SupportConfig{}, fmt.Errorf("config: FEATURE_SETTLEMENT_HOLD_LEDGER_ENABLED must be true or false")
		}
		cfg.HoldLedger = v
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
