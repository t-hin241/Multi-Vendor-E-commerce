// Package config extends the shared base config with the settings unique to
// Vendor: the JWT secret, where to reach Notification for approval
// decisions, and how to reach the object store for shop logo/banner images.
package config

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strconv"

	"shopee/backend/pkg/config"
	"shopee/backend/pkg/platform/objectstorage"
)

type Config struct {
	PayoutKey                        []byte
	PayoutServiceKey                 string
	Base                             config.Base
	Internal                         config.InternalServices
	CatalogURL, OrderURL, PaymentURL string
	NotificationServiceURL           string
	ObjectStorage                    objectstorage.Config
	// VersionedPolicies is FEATURE_VERSIONED_POLICIES_ENABLED (AF-02).
	VersionedPolicies bool
	// ShopStaff is FEATURE_SHOP_STAFF_ENABLED (AF-17); StaffInvitesPaused
	// is SHOP_STAFF_INVITES_PAUSED.
	ShopStaff, StaffInvitesPaused bool
	// StaffFingerprintKey keys the hash stored instead of an invited
	// address; StaffAcceptURL is the page the invitation link opens.
	StaffFingerprintKey []byte
	StaffAcceptURL      string
	// AdminReauth is FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED (AF-19):
	// payout destination decisions and detail reads need a password proof.
	AdminReauth bool
}

func Load() (Config, error) {
	base, err := config.LoadBase("vendor", "8082")
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

	payoutRaw, err := requireEnv("VENDOR_PAYOUT_ENCRYPTION_KEY")
	if err != nil {
		return Config{}, err
	}
	payoutKey, err := base64.StdEncoding.DecodeString(payoutRaw)
	if err != nil || len(payoutKey) != 32 {
		return Config{}, fmt.Errorf("VENDOR_PAYOUT_ENCRYPTION_KEY must be base64 for exactly 32 bytes")
	}
	payoutServiceKey, err := requireEnv("VENDOR_PAYOUT_SERVICE_KEY")
	if err != nil || len(payoutServiceKey) < 32 {
		return Config{}, fmt.Errorf("VENDOR_PAYOUT_SERVICE_KEY must contain at least 32 characters")
	}
	internal, err := config.LoadInternalServices()
	if err != nil {
		return Config{}, err
	}
	if payoutServiceKey == internal.Key {
		return Config{}, fmt.Errorf("payout scope key must differ from internal service key")
	}
	versioned := false
	if raw := os.Getenv("FEATURE_VERSIONED_POLICIES_ENABLED"); raw != "" {
		if versioned, err = strconv.ParseBool(raw); err != nil {
			return Config{}, fmt.Errorf("config: FEATURE_VERSIONED_POLICIES_ENABLED must be true or false")
		}
	}
	staff, paused, staffKey, acceptURL, err := loadShopStaff(base.Env, internal.Key, payoutServiceKey)
	if err != nil {
		return Config{}, err
	}
	adminReauth := false
	if raw := os.Getenv("FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED"); raw != "" {
		if adminReauth, err = strconv.ParseBool(raw); err != nil {
			return Config{}, fmt.Errorf("config: FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED must be true or false")
		}
	}
	return Config{
		AdminReauth: adminReauth,
		ShopStaff:   staff, StaffInvitesPaused: paused, StaffFingerprintKey: staffKey, StaffAcceptURL: acceptURL,
		VersionedPolicies: versioned,
		PaymentURL:        envDefault("PAYMENT_SERVICE_URL", "http://payment:8087"), PayoutKey: payoutKey, PayoutServiceKey: payoutServiceKey, Internal: internal, CatalogURL: envDefault("CATALOG_SERVICE_URL", "http://catalog:8083"), OrderURL: envDefault("ORDER_SERVICE_URL", "http://order:8086"),
		Base:                   base,
		NotificationServiceURL: notificationServiceURL,
		ObjectStorage:          objectStorageCfg,
	}, nil
}

// loadShopStaff reads the AF-17 settings. The fingerprint key and accept
// URL are required only when the feature is on; the URL carries the token
// in its fragment, so it must have none of its own and use HTTPS outside
// local development.
func loadShopStaff(env, internalKey, payoutServiceKey string) (enabled, paused bool, key []byte, acceptURL string, err error) {
	for name, target := range map[string]*bool{"FEATURE_SHOP_STAFF_ENABLED": &enabled, "SHOP_STAFF_INVITES_PAUSED": &paused} {
		if raw := os.Getenv(name); raw != "" {
			if *target, err = strconv.ParseBool(raw); err != nil {
				return false, false, nil, "", fmt.Errorf("config: %s must be true or false", name)
			}
		}
	}
	rawKey := os.Getenv("STAFF_INVITATION_FINGERPRINT_KEY")
	acceptURL = os.Getenv("STAFF_INVITATION_ACCEPT_URL")
	if !enabled {
		// Off: invitations are never created, so the placeholders of
		// .env.example must not stop the service.
		return false, paused, nil, "", nil
	}
	if len(rawKey) < 32 || rawKey == "CHANGE_ME" || rawKey == internalKey || rawKey == payoutServiceKey {
		return false, false, nil, "", fmt.Errorf("config: STAFF_INVITATION_FINGERPRINT_KEY must be a distinct secret of at least 32 characters")
	}
	u, perr := url.Parse(acceptURL)
	secure := env == "production" || env == "staging"
	if perr != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Scheme != "https" && (secure || u.Scheme != "http")) {
		return false, false, nil, "", fmt.Errorf("config: STAFF_INVITATION_ACCEPT_URL must be an absolute URL without query/fragment, HTTPS in staging/production")
	}
	return enabled, paused, []byte(rawKey), acceptURL, nil
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

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
