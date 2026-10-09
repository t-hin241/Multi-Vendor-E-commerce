// Package shopaccess is the shop-scoped permission contract (AF-17).
//
// Vendor owns shop memberships. Every service that lets a person act for a
// shop asks Vendor, per request, whether that person holds one named
// permission on that shop: POST /internal/vendors/authorize. Nothing here
// caches a positive answer across requests, and every doubt (timeout,
// unreadable answer, unknown permission) is a refusal: a failed check is
// 503 authorization_unavailable, a refusal is 403 permission_denied.
package shopaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
)

// Permission names (registry v1). A permission outside this list is never
// granted, and asking for one is a refusal.
const (
	ProductsRead          = "products.read"
	ProductsWrite         = "products.write"
	InventoryRead         = "inventory.read"
	InventoryAdjust       = "inventory.adjust"
	OrdersRead            = "orders.read"
	OrdersFulfill         = "orders.fulfill"
	ReturnsHandle         = "returns.handle"
	FinanceRead           = "finance.read"
	StaffManage           = "staff.manage"
	SupportReply          = "support.reply"
	ShopAvailabilityWrite = "shop.availability.write"
	AnalyticsRead         = "analytics.read"
	MarketingManage       = "marketing.manage"
	LiveHost              = "live.host"

	// PayoutDestinationWrite and ShopSettingsWrite belong to the owner
	// alone; v1 never grants them to staff.
	PayoutDestinationWrite = "payout_destination.write"
	ShopSettingsWrite      = "shop.settings.write"
)

// Definition describes one registry entry. Available is false while the
// feature the permission would unlock does not exist yet: such a
// permission cannot be granted, so turning the feature on later never
// hands out access nobody chose.
type Definition struct {
	Name      string `json:"name"`
	OwnerOnly bool   `json:"owner_only"`
	Available bool   `json:"available"`
}

var registry = []Definition{
	{Name: ProductsRead, Available: true},
	{Name: ProductsWrite, Available: true},
	{Name: InventoryRead, Available: true},
	{Name: InventoryAdjust, Available: true},
	{Name: OrdersRead, Available: true},
	{Name: OrdersFulfill, Available: true},
	{Name: ReturnsHandle, Available: true},
	{Name: FinanceRead, Available: true},
	{Name: StaffManage, Available: true},
	{Name: SupportReply, Available: true},
	{Name: ShopAvailabilityWrite},
	{Name: AnalyticsRead, Available: true},
	{Name: MarketingManage},
	{Name: LiveHost},
	{Name: PayoutDestinationWrite, OwnerOnly: true, Available: true},
	{Name: ShopSettingsWrite, OwnerOnly: true, Available: true},
}

// Registry returns a copy of the permission registry.
func Registry() []Definition { return append([]Definition(nil), registry...) }

func lookup(name string) (Definition, bool) {
	for _, d := range registry {
		if d.Name == name {
			return d, true
		}
	}
	return Definition{}, false
}

// Known reports whether name is in the registry.
func Known(name string) bool {
	_, ok := lookup(name)
	return ok
}

// Grantable reports whether name may be granted to staff.
func Grantable(name string) bool {
	d, ok := lookup(name)
	return ok && d.Available && !d.OwnerOnly
}

// Grant is a positive answer for one request. MembershipVersion is the
// membership version the answer was based on (0 for the owner path of an
// older Vendor), for audit next to the mutation it allowed.
type Grant struct {
	VendorID          string
	Status            string
	VendorVersion     int64
	Role              string
	MembershipVersion int64
}

const (
	CodePermissionDenied         apperror.Code = "permission_denied"
	CodeAuthorizationUnavailable apperror.Code = "authorization_unavailable"
)

// Denied is the refusal error (403 permission_denied).
func Denied(message string) *apperror.Error {
	if message == "" {
		message = "You do not have permission for this shop action"
	}
	return &apperror.Error{Code: CodePermissionDenied, Message: message, Status: http.StatusForbidden}
}

// Unavailable is the fail-closed error when the answer is unknown.
func Unavailable(err error) *apperror.Error {
	return &apperror.Error{Code: CodeAuthorizationUnavailable, Message: "Shop permissions cannot be checked right now; please try again shortly",
		Status: http.StatusServiceUnavailable, Err: err}
}

// IsUnavailable reports whether err is a failed (not refused) check.
func IsUnavailable(err error) bool {
	var app *apperror.Error
	return errors.As(err, &app) && app.Code == CodeAuthorizationUnavailable
}

// Client calls Vendor's authorize contract. HTTP may be nil.
type Client struct {
	URL, Key string
	HTTP     *http.Client
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return telemetry.NewHTTPClient(3 * time.Second)
}

type authorizeRequest struct {
	ActorUserID string `json:"actor_user_id"`
	VendorID    string `json:"vendor_id"`
	Permission  string `json:"permission"`
}

type authorizeResponse struct {
	Data struct {
		Allowed           bool   `json:"allowed"`
		VendorID          string `json:"vendor_id"`
		Status            string `json:"status"`
		VendorVersion     int64  `json:"vendor_version"`
		Role              string `json:"role"`
		MembershipVersion int64  `json:"membership_version"`
	} `json:"data"`
}

// Authorize asks Vendor whether actorUserID holds permission on vendorID.
// The caller derives vendorID from the resource it is about to touch
// (never from a client claim alone) and applies its own shop-status rule
// to Grant.Status.
func (c Client) Authorize(ctx context.Context, actorUserID, vendorID, permission string) (Grant, error) {
	if _, err := uuid.Parse(actorUserID); err != nil {
		return Grant{}, Denied("")
	}
	if _, err := uuid.Parse(vendorID); err != nil {
		return Grant{}, Denied("")
	}
	if !Known(permission) {
		return Grant{}, Denied("")
	}
	body, err := json.Marshal(authorizeRequest{ActorUserID: actorUserID, VendorID: vendorID, Permission: permission})
	if err != nil {
		return Grant{}, Unavailable(err)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+"/internal/vendors/authorize", bytes.NewReader(body))
	if err != nil {
		return Grant{}, Unavailable(err)
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, c.Key)
	resp, err := c.http().Do(req)
	if err != nil {
		return Grant{}, Unavailable(err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		// A Vendor without this contract yet (rolling deploy): fall back
		// to the owner-only lookup, which can never grant staff access.
		return c.ownerOnly(ctx, actorUserID, vendorID)
	case http.StatusBadRequest, http.StatusForbidden:
		return Grant{}, Denied("")
	default:
		return Grant{}, Unavailable(fmt.Errorf("vendor authorize answered %d", resp.StatusCode))
	}
	var out authorizeResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&out); err != nil {
		return Grant{}, Unavailable(err)
	}
	if !out.Data.Allowed {
		return Grant{}, Denied("")
	}
	if out.Data.VendorID != vendorID || out.Data.Status == "" {
		return Grant{}, Unavailable(errors.New("vendor authorize answered for another shop"))
	}
	g := Grant{VendorID: out.Data.VendorID, Status: out.Data.Status, VendorVersion: out.Data.VendorVersion,
		Role: out.Data.Role, MembershipVersion: out.Data.MembershipVersion}
	remember(ctx, g) // PW-021: the audit records which grant allowed the change
	return g, nil
}

func (c Client) ownerOnly(ctx context.Context, actorUserID, vendorID string) (Grant, error) {
	endpoint := fmt.Sprintf("%s/internal/vendors/%s/owned-by/%s", strings.TrimRight(c.URL, "/"), url.PathEscape(vendorID), url.PathEscape(actorUserID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Grant{}, Unavailable(err)
	}
	serviceauth.SetRequestHeaders(req, c.Key)
	resp, err := c.http().Do(req)
	if err != nil {
		return Grant{}, Unavailable(err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden:
		return Grant{}, Denied("")
	case resp.StatusCode != http.StatusOK:
		return Grant{}, Unavailable(fmt.Errorf("vendor ownership answered %d", resp.StatusCode))
	}
	var out struct {
		Data struct {
			VendorID string `json:"vendor_id"`
			Status   string `json:"status"`
			Version  int64  `json:"version"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&out); err != nil {
		return Grant{}, Unavailable(err)
	}
	if out.Data.VendorID != vendorID || out.Data.Status == "" {
		return Grant{}, Unavailable(errors.New("vendor ownership answered for another shop"))
	}
	// A Vendor without memberships: version 0 marks the owner-only check.
	g := Grant{VendorID: vendorID, Status: out.Data.Status, VendorVersion: out.Data.Version, Role: "owner"}
	remember(ctx, g)
	return g, nil
}
