package domain_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"shopee/backend/services/notification/internal/domain"
)

const shop = "22222222-2222-4222-8222-222222222222"

// Each producer may report only its own kinds, and each kind maps to one
// purpose and template.
func TestVendorActionAllowlist(t *testing.T) {
	now := time.Now()
	cases := []struct {
		source, kind, purpose string
		notice                domain.Type
	}{
		{"order", "new_order", domain.PurposeOrders, domain.TypeVendorNewOrder},
		{"order", "cancellation_requested", domain.PurposeOrders, domain.TypeVendorCancellationRequested},
		{"order", "return_requested", domain.PurposeReturns, domain.TypeVendorReturnRequested},
		{"order", "return_dispatched", domain.PurposeReturns, domain.TypeVendorReturnDispatched},
		{"payment", "payout_succeeded", domain.PurposeFinance, domain.TypeVendorPayoutSucceeded},
		{"payment", "payout_failed", domain.PurposeFinance, domain.TypeVendorPayoutFailed},
		{"order", "support_case_opened", domain.PurposeSupport, domain.TypeVendorSupportCaseOpened},
		{"order", "support_waiting_shop", domain.PurposeSupport, domain.TypeVendorSupportWaitingShop},
		{"order", "delivery_goods_returned", domain.PurposeOrders, domain.TypeVendorDeliveryGoodsReturned},
		{"order", "redelivery_accepted", domain.PurposeOrders, domain.TypeVendorRedeliveryAccepted},
	}
	for _, c := range cases {
		a, err := domain.NewVendorAction(domain.VendorActionRequest{Source: c.source, EventID: "effect-1", VendorID: shop, ActionKind: c.kind, ReferenceID: "ref-1"}, now)
		if err != nil {
			t.Fatalf("%s/%s: %v", c.source, c.kind, err)
		}
		if a.Purpose != c.purpose || a.Notice() != c.notice || a.Status != domain.VendorActionPending {
			t.Fatalf("%s/%s maps to %s/%s", c.source, c.kind, a.Purpose, a.Notice())
		}
	}
	for _, bad := range []domain.VendorActionRequest{
		{Source: "payment", EventID: "e", VendorID: shop, ActionKind: "new_order", ReferenceID: "r"},
		{Source: "order", EventID: "e", VendorID: shop, ActionKind: "payout_failed", ReferenceID: "r"},
		{Source: "vendor", EventID: "e", VendorID: shop, ActionKind: "new_order", ReferenceID: "r"},
		{Source: "order", EventID: "e", VendorID: "not-a-shop", ActionKind: "new_order", ReferenceID: "r"},
		{Source: "order", EventID: "", VendorID: shop, ActionKind: "new_order", ReferenceID: "r"},
		{Source: "order", EventID: "e", VendorID: shop, ActionKind: "new_order", ReferenceID: "has space"},
		{Source: "order", EventID: "e", VendorID: shop, ActionKind: "new_order", ReferenceID: "r", VendorOrderID: "x"},
	} {
		if _, err := domain.NewVendorAction(bad, now); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

// The owner is always told; staff only for what they opted into; a person
// listed twice is told once.
func TestSelectRecipients(t *testing.T) {
	owner, clerk, accountant := "u-owner", "u-clerk", "u-accountant"
	candidates := []domain.Candidate{{UserID: owner, Role: "owner"}, {UserID: clerk, Role: "staff"}, {UserID: accountant, Role: "staff"},
		{UserID: owner, Role: "staff"}}
	optIn := map[string][]string{clerk: {domain.PurposeOrders}, accountant: {domain.PurposeFinance}}
	if got := domain.SelectRecipients(domain.PurposeOrders, candidates, optIn); !slices.Equal(got, []string{clerk, owner}) {
		t.Fatalf("orders: %v", got)
	}
	if got := domain.SelectRecipients(domain.PurposeReturns, candidates, optIn); !slices.Equal(got, []string{owner}) {
		t.Fatalf("returns: %v", got)
	}
	if got := domain.SelectRecipients(domain.PurposeFinance, candidates[1:2], optIn); len(got) != 0 {
		t.Fatalf("staff without opt-in told: %v", got)
	}
	if got, err := domain.NormalizeVendorCategories([]string{"returns", "orders", "orders"}); err != nil || !slices.Equal(got, []string{"orders", "returns"}) {
		t.Fatalf("normalize: %v %v", got, err)
	}
	if _, err := domain.NormalizeVendorCategories([]string{"marketing"}); err == nil {
		t.Fatal("unknown category accepted")
	}
}

// Shop notices name the reference and the console page, never an amount,
// an account or a full URL.
func TestVendorNoticesRender(t *testing.T) {
	for _, typ := range []domain.Type{domain.TypeVendorNewOrder, domain.TypeVendorCancellationRequested, domain.TypeVendorReturnRequested,
		domain.TypeVendorReturnDispatched, domain.TypeVendorPayoutSucceeded, domain.TypeVendorPayoutFailed, domain.TypeVendorSupportCaseOpened,
		domain.TypeVendorSupportWaitingShop, domain.TypeVendorDeliveryGoodsReturned, domain.TypeVendorRedeliveryAccepted} {
		n, err := domain.NewNotification(domain.Request{EventID: "effect-1", Source: "order", UserID: user, Type: typ, ReferenceID: "0123456789abcdef"}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		subject, body, err := domain.Render(n, "Chủ shop")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(subject, "01234567") || !strings.Contains(body, "/vendor") || strings.Contains(body, "http") ||
			strings.ContainsAny(body, "₫$") || strings.Contains(strings.ToLower(body), "vnd") {
			t.Fatalf("%s renders badly: %q %q", typ, subject, body)
		}
	}
}
