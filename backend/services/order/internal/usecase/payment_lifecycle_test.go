package usecase_test

import (
	"testing"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
)

func TestMarkPaid_TransitionsOrderAndVendorOrdersAndCommitsStock(t *testing.T) {
	f := newCheckoutFixture()
	ctx := t.Context()
	f.cart.byToken[testToken] = []adapter.CartLine{
		{ProductID: "p1", Quantity: 1},
		{ProductID: "p2", Quantity: 1},
	}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "vendor-a", Name: "Shoe", PriceAmount: 1000, Currency: "VND", IsVisible: true}
	f.catalog.products["p2"] = &adapter.ProductInfo{ID: "p2", VendorID: "vendor-b", Name: "Hat", PriceAmount: 500, Currency: "VND", IsVisible: true}

	order, err := f.uc.Checkout(ctx, "buyer-1", testToken, f.addressID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	paid, err := f.uc.MarkPaid(ctx, order.ID)
	if err != nil {
		t.Fatalf("unexpected error marking paid: %v", err)
	}
	if paid.Status != domain.StatusPaid {
		t.Errorf("expected order status paid, got %q", paid.Status)
	}
	if !f.inventory.committedOrders[order.ID] {
		t.Error("expected the stock reservation to be committed once payment succeeds")
	}
	if len(f.notifications.sent) != 1 || f.notifications.sent[0].notifType != "order_paid" {
		t.Errorf("expected one order_paid notification to the buyer, got %+v", f.notifications.sent)
	}

	vendorOrders, _ := f.vendorOrders.ListByOrderID(ctx, order.ID)
	for _, vo := range vendorOrders {
		if vo.Status != domain.StatusPaid {
			t.Errorf("expected vendor order %s to be paid, got %q", vo.ID, vo.Status)
		}
		if vo.CommissionRateBps == nil || *vo.CommissionRateBps != 1000 {
			t.Errorf("expected vendor order %s to snapshot the 1000bps commission rate, got %v", vo.ID, vo.CommissionRateBps)
		}
		wantCommission, wantNet := domain.ComputeCommission(vo.SubtotalAmount, 1000)
		if vo.CommissionAmount == nil || *vo.CommissionAmount != wantCommission {
			t.Errorf("expected commission_amount %d, got %v", wantCommission, vo.CommissionAmount)
		}
		if vo.NetAmount == nil || *vo.NetAmount != wantNet {
			t.Errorf("expected net_amount %d, got %v", wantNet, vo.NetAmount)
		}
	}

	// Idempotent: a retried webhook delivery must not error.
	again, err := f.uc.MarkPaid(ctx, order.ID)
	if err != nil {
		t.Fatalf("expected marking an already-paid order paid to be a no-op, got error: %v", err)
	}
	if again.Status != domain.StatusPaid {
		t.Errorf("expected still paid, got %q", again.Status)
	}
}

func TestMarkPaymentFailed_CancelsOrderAndReleasesStock(t *testing.T) {
	f := newCheckoutFixture()
	ctx := t.Context()
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", Quantity: 1}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "vendor-a", Name: "Shoe", PriceAmount: 1000, Currency: "VND", IsVisible: true}

	order, err := f.uc.Checkout(ctx, "buyer-1", testToken, f.addressID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	failed, err := f.uc.MarkPaymentFailed(ctx, order.ID, "card declined")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if failed.Status != domain.StatusCancelled {
		t.Errorf("expected cancelled, got %q", failed.Status)
	}
	if !f.inventory.releasedOrders[order.ID] {
		t.Error("expected inventory to be released after a failed payment")
	}
}

func TestMarkPaid_ConflictWhenOrderAlreadyCancelled(t *testing.T) {
	f := newCheckoutFixture()
	ctx := t.Context()
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", Quantity: 1}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "vendor-a", Name: "Shoe", PriceAmount: 1000, Currency: "VND", IsVisible: true}

	order, err := f.uc.Checkout(ctx, "buyer-1", testToken, f.addressID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.uc.Cancel(ctx, "buyer-1", order.ID); err != nil {
		t.Fatalf("unexpected error cancelling: %v", err)
	}

	_, err = f.uc.MarkPaid(ctx, order.ID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict marking a cancelled order paid, got %v", appErr.Code)
	}
}

func TestUpdateVendorOrderStatus_WeakestLinkGatesOverallOrder(t *testing.T) {
	f := newCheckoutFixture()
	ctx := t.Context()
	f.cart.byToken[testToken] = []adapter.CartLine{
		{ProductID: "p1", Quantity: 1},
		{ProductID: "p2", Quantity: 1},
	}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "vendor-a", Name: "Shoe", PriceAmount: 1000, Currency: "VND", IsVisible: true}
	f.catalog.products["p2"] = &adapter.ProductInfo{ID: "p2", VendorID: "vendor-b", Name: "Hat", PriceAmount: 500, Currency: "VND", IsVisible: true}
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	f.vendors.approvedVendors["user-b"] = "vendor-b"

	order, err := f.uc.Checkout(ctx, "buyer-1", testToken, f.addressID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.uc.MarkPaid(ctx, order.ID); err != nil {
		t.Fatalf("unexpected error marking paid: %v", err)
	}

	vendorOrders, _ := f.vendorOrders.ListByOrderID(ctx, order.ID)
	var voA, voB *domain.VendorOrder
	for _, vo := range vendorOrders {
		if vo.VendorID == "vendor-a" {
			voA = vo
		} else {
			voB = vo
		}
	}
	if voA == nil || voB == nil {
		t.Fatal("expected one vendor order per vendor")
	}

	// Vendor A ships their part; the overall order stays "paid" because
	// vendor B hasn't moved yet — the buyer only sees progress once every
	// vendor has made it.
	if _, err := f.uc.UpdateVendorOrderStatus(ctx, "user-a", voA.ID, domain.StatusProcessing); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.uc.UpdateVendorOrderStatus(ctx, "user-a", voA.ID, domain.StatusShipped); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stillPaid, err := f.uc.GetForInternal(ctx, order.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stillPaid.Status != domain.StatusPaid {
		t.Errorf("expected order to stay paid while vendor B hasn't shipped, got %q", stillPaid.Status)
	}

	// Vendor B can't skip straight to shipped without processing first.
	if _, err := f.uc.UpdateVendorOrderStatus(ctx, "user-b", voB.ID, domain.StatusShipped); err == nil {
		t.Error("expected an error skipping straight from paid to shipped")
	}
	if _, err := f.uc.UpdateVendorOrderStatus(ctx, "user-b", voB.ID, domain.StatusProcessing); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.uc.UpdateVendorOrderStatus(ctx, "user-b", voB.ID, domain.StatusShipped); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	nowShipped, err := f.uc.GetForInternal(ctx, order.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nowShipped.Status != domain.StatusShipped {
		t.Errorf("expected order shipped once every vendor has shipped, got %q", nowShipped.Status)
	}
}

func TestUpdateVendorOrderStatus_RejectsNonOwningVendor(t *testing.T) {
	f := newCheckoutFixture()
	ctx := t.Context()
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", Quantity: 1}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "vendor-a", Name: "Shoe", PriceAmount: 1000, Currency: "VND", IsVisible: true}
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	f.vendors.approvedVendors["user-other"] = "vendor-other"

	order, err := f.uc.Checkout(ctx, "buyer-1", testToken, f.addressID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.uc.MarkPaid(ctx, order.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	vendorOrders, _ := f.vendorOrders.ListByOrderID(ctx, order.ID)

	_, err = f.uc.UpdateVendorOrderStatus(ctx, "user-other", vendorOrders[0].ID, domain.StatusProcessing)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden for a non-owning vendor, got %v", appErr.Code)
	}
}

func TestAdminTransition_RefundsAPaidOrder(t *testing.T) {
	f := newCheckoutFixture()
	ctx := t.Context()
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", Quantity: 1}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "vendor-a", Name: "Shoe", PriceAmount: 1000, Currency: "VND", IsVisible: true}

	order, err := f.uc.Checkout(ctx, "buyer-1", testToken, f.addressID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.uc.MarkPaid(ctx, order.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	refunded, err := f.uc.AdminTransition(ctx, order.ID, domain.StatusRefunded, "buyer dispute")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if refunded.Status != domain.StatusRefunded {
		t.Errorf("expected refunded, got %q", refunded.Status)
	}
}

func TestAdminTransition_RejectsDisallowedTarget(t *testing.T) {
	f := newCheckoutFixture()
	ctx := t.Context()
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", Quantity: 1}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "vendor-a", Name: "Shoe", PriceAmount: 1000, Currency: "VND", IsVisible: true}

	order, err := f.uc.Checkout(ctx, "buyer-1", testToken, f.addressID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = f.uc.AdminTransition(ctx, order.ID, domain.StatusPaid, "trying to skip payment")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error, admin can only cancel/refund, got %v", appErr.Code)
	}
}
