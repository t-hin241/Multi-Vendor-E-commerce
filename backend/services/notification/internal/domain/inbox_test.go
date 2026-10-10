package domain_test

import (
	"strings"
	"testing"
	"time"

	"shopee/backend/services/notification/internal/domain"
)

// Every notice kind people receive has an inbox item with plain text and
// an app route; the route carries the reference, never a full URL.
func TestInboxItemsForEveryKind(t *testing.T) {
	ref := "11111111-2222-4333-8444-555555555555"
	for _, typ := range []domain.Type{domain.TypeOrderPaid, domain.TypeOrderRefunded, domain.TypeReturnShippingInstructions, domain.TypeVendorApproved,
		domain.TypeVendorNewOrder, domain.TypeVendorPayoutFailed, "sla_support", "sla_delivery_exception",
		domain.TypeVendorSupportReplyDue, domain.TypeVendorSupportReplyOverdue, domain.TypeVendorGoodsReceiptDue, domain.TypeVendorGoodsReceiptOverdue,
		domain.TypeSupportIntakeReceived, domain.TypeSupportIntakeClosed, domain.TypeRefundDestinationNeeded, domain.TypeRefundDestinationRejected} {
		n, err := domain.NewNotification(domain.Request{EventID: "e-1", Source: "order", UserID: user, Type: typ, ReferenceID: ref}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		item, ok, err := domain.NewInboxItem(n)
		if err != nil || !ok {
			t.Fatalf("%s: %v %v", typ, ok, err)
		}
		if !strings.HasPrefix(item.Link, "/") || strings.Contains(item.Link, "//") || item.Title == "" || strings.Contains(item.Body, "Xin chào") ||
			item.RecipientID != user || item.ReferenceID != ref || item.ReferenceType == "" {
			t.Fatalf("%s: %+v", typ, item)
		}
	}
	n, _ := domain.NewNotification(domain.Request{UserID: user, Type: domain.TypeOrderPaid, ReferenceID: "order-1"}, time.Now())
	item, _, _ := domain.NewInboxItem(n)
	if item.Link != "/orders/order-1" || !strings.Contains(item.Title, "order-1") {
		t.Fatalf("order link %+v", item)
	}
	// PW-009: a refund destination opens the order page (where the buyer
	// gives the account); a request without an order opens /support.
	for typ, link := range map[domain.Type]string{domain.TypeRefundDestinationRejected: "/orders/order-1", domain.TypeSupportIntakeClosed: "/support",
		domain.TypeVendorSupportReplyOverdue: "/vendor/support/order-1"} {
		n, _ := domain.NewNotification(domain.Request{UserID: user, Type: typ, ReferenceID: "order-1"}, time.Now())
		item, _, _ := domain.NewInboxItem(n)
		if item.Link != link || strings.Contains(item.Body, "••••") {
			t.Fatalf("%s link %+v", typ, item)
		}
	}
}

func TestInboxCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 9, 8, 0, 0, 123456789, time.UTC)
	c := domain.InboxCursor{CreatedAt: at, ID: "11111111-2222-4333-8444-555555555555"}
	got, err := domain.DecodeInboxCursor(c.Encode())
	if err != nil || !got.CreatedAt.Equal(at) || got.ID != c.ID {
		t.Fatalf("round trip %+v %v", got, err)
	}
	for _, bad := range []string{"", "not base64!", "Zm9v", c.Encode() + "x"} {
		if _, err := domain.DecodeInboxCursor(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
