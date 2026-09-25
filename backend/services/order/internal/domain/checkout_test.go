package domain_test

import (
	"testing"

	"shopee/backend/services/order/internal/domain"
)

func TestBuildCheckoutPlan_RejectsEmptyCart(t *testing.T) {
	_, err := domain.BuildCheckoutPlan("buyer-1", nil)
	if err == nil {
		t.Fatal("expected an error for an empty checkout")
	}
}

func TestBuildCheckoutPlan_RejectsMixedCurrency(t *testing.T) {
	lines := []domain.CheckoutLine{
		{ProductID: "p1", VendorID: "v1", PriceAmount: 100, Currency: "VND", Quantity: 1},
		{ProductID: "p2", VendorID: "v1", PriceAmount: 100, Currency: "USD", Quantity: 1},
	}

	_, err := domain.BuildCheckoutPlan("buyer-1", lines)
	if err == nil {
		t.Fatal("expected an error for mixed currencies")
	}
}

func TestBuildCheckoutPlan_SplitsByVendor(t *testing.T) {
	lines := []domain.CheckoutLine{
		{ProductID: "p1", VendorID: "vendor-a", ProductName: "Shoe", PriceAmount: 100000, Currency: "VND", Quantity: 2},
		{ProductID: "p2", VendorID: "vendor-b", ProductName: "Hat", PriceAmount: 50000, Currency: "VND", Quantity: 1},
		{ProductID: "p3", VendorID: "vendor-a", ProductName: "Sock", PriceAmount: 20000, Currency: "VND", Quantity: 3},
	}

	plan, err := domain.BuildCheckoutPlan("buyer-1", lines)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.VendorOrders) != 2 {
		t.Fatalf("expected 2 vendor orders, got %d", len(plan.VendorOrders))
	}
	if len(plan.Items) != 3 {
		t.Fatalf("expected 3 order items, got %d", len(plan.Items))
	}

	// vendor-a: 100000*2 + 20000*3 = 260000; vendor-b: 50000*1 = 50000
	wantTotal := int64(260000 + 50000)
	if plan.Order.TotalAmount != wantTotal {
		t.Errorf("expected total %d, got %d", wantTotal, plan.Order.TotalAmount)
	}

	var vendorATotal, vendorBTotal int64
	for _, vo := range plan.VendorOrders {
		switch vo.VendorID {
		case "vendor-a":
			vendorATotal = vo.SubtotalAmount
		case "vendor-b":
			vendorBTotal = vo.SubtotalAmount
		}
	}
	if vendorATotal != 260000 {
		t.Errorf("expected vendor-a subtotal 260000, got %d", vendorATotal)
	}
	if vendorBTotal != 50000 {
		t.Errorf("expected vendor-b subtotal 50000, got %d", vendorBTotal)
	}
}

func TestBuildCheckoutPlan_SnapshotsPriceIndependentOfLaterChanges(t *testing.T) {
	line := domain.CheckoutLine{ProductID: "p1", VendorID: "v1", ProductName: "Shoe", PriceAmount: 100000, Currency: "VND", Quantity: 1}

	plan, err := domain.BuildCheckoutPlan("buyer-1", []domain.CheckoutLine{line})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Mutate the original line after building the plan; the plan must not
	// be affected, since CheckoutLine is passed by value and the snapshot
	// is already computed.
	line.PriceAmount = 999999

	if plan.Items[0].PriceAmount != 100000 {
		t.Errorf("expected snapshotted price 100000, got %d", plan.Items[0].PriceAmount)
	}
}
