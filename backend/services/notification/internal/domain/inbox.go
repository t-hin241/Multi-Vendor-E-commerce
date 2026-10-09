package domain

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// AF-09: a person's inbox is a read model of the notices Notification
// records: what happened, readable even without the email. It never
// confirms money or delivery by itself; the item links to the owner's page,
// which checks access again when opened.

// InboxItem is one notice in one person's inbox.
type InboxItem struct {
	ID              string
	RecipientID     string
	Source          string
	EventID         string
	Kind            Type
	TemplateVersion string
	Title           string
	Body            string
	ReferenceType   string
	ReferenceID     string
	Link            string
	ReadAt          *time.Time
	HiddenAt        *time.Time
	CreatedAt       time.Time
}

type inboxTarget struct {
	referenceType string
	// link is an app route; %s is the full reference when present.
	link string
}

// inboxTargets says what each notice is about and where it opens. A kind
// missing here gets no inbox item (the email is still sent).
var inboxTargets = map[Type]inboxTarget{
	TypeOrderPaid:                  {"order", "/orders/%s"},
	TypeOrderShipped:               {"order", "/orders/%s"},
	TypeOrderCompleted:             {"order", "/orders/%s"},
	TypeOrderCancelled:             {"order", "/orders/%s"},
	TypeOrderRefunded:              {"order", "/orders/%s"},
	TypeSupportCaseOpened:          {"order", "/orders/%s"},
	TypeSupportCaseResolved:        {"order", "/orders/%s"},
	TypeCancellationRequested:      {"order", "/orders/%s"},
	TypeCancellationApproved:       {"order", "/orders/%s"},
	TypeCancellationRejected:       {"order", "/orders/%s"},
	TypeDeliveryExceptionOpened:    {"order", "/orders/%s"},
	TypeRedeliveryOffered:          {"order", "/orders/%s"},
	TypeDeliveryExceptionResolved:  {"order", "/orders/%s"},
	TypeReturnShippingInstructions: {"order", "/orders/%s"},
	TypeVendorApproved:             {"shop", "/vendor/shops"},
	TypeVendorRejected:             {"shop", "/vendor/shops"},
	TypeMarketplacePolicyUpdated:   {"shop", "/vendor/shops"},
	TypeShopPolicyApproved:         {"shop", "/vendor/shops"},
	TypeShopPolicyRejected:         {"shop", "/vendor/shops"},

	TypeVendorNewOrder:              {"vendor_order", "/vendor/orders"},
	TypeVendorCancellationRequested: {"cancellation", "/vendor/orders"},
	TypeVendorReturnRequested:       {"return", "/vendor/orders"},
	TypeVendorReturnDispatched:      {"return", "/vendor/orders"},
	TypeVendorPayoutSucceeded:       {"payout", "/vendor"},
	TypeVendorPayoutFailed:          {"payout", "/vendor/payout-accounts"},

	"sla_support":            {"support_case", "/admin/support/%s"},
	"sla_return":             {"return", "/admin/returns?return_id=%s"},
	"sla_refund":             {"refund", "/admin/refunds?refund_id=%s"},
	"sla_cancellation":       {"cancellation", "/admin/cancellations?request_id=%s"},
	"sla_interception":       {"shipment", "/admin/fulfillment?shipment_id=%s"},
	"sla_delivery_exception": {"delivery_exception", "/admin/delivery-exceptions?exception_id=%s"},
}

var inboxLinkPattern = regexp.MustCompile(`^/[A-Za-z0-9/_?=&.-]{0,300}$`)

// NewInboxItem builds the inbox item of a recorded notice: the same plain
// text as the email (no greeting), the reference and the app route. ok is
// false when the kind has no inbox item.
func NewInboxItem(n *Notification) (item *InboxItem, ok bool, err error) {
	target, ok := inboxTargets[n.Type]
	if !ok {
		return nil, false, nil
	}
	title, body, err := renderText(n)
	if err != nil {
		return nil, false, err
	}
	link := target.link
	if strings.Contains(link, "%s") {
		link = fmt.Sprintf(link, n.ReferenceID)
	}
	if !inboxLinkPattern.MatchString(link) {
		return nil, false, fmt.Errorf("inbox link for %s is not an app route", n.Type)
	}
	return &InboxItem{RecipientID: n.UserID, Source: n.Source, EventID: n.EventID, Kind: n.Type, TemplateVersion: n.TemplateVersion,
		Title: clip(title, 200), Body: clip(body, 1000), ReferenceType: target.referenceType, ReferenceID: n.ReferenceID, Link: link}, true, nil
}

func clip(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// InboxCursor is a position in a person's inbox, newest first.
type InboxCursor struct {
	CreatedAt time.Time
	ID        string
}

// Encode makes the opaque cursor handed to the client.
func (c InboxCursor) Encode() string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID))
}

// DecodeInboxCursor reads a cursor from the client; it never grants
// access, the recipient comes from the session.
func DecodeInboxCursor(raw string) (*InboxCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor")
	}
	at, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return nil, fmt.Errorf("invalid cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil || !referencePattern.MatchString(id) {
		return nil, fmt.Errorf("invalid cursor")
	}
	return &InboxCursor{CreatedAt: t, ID: id}, nil
}
