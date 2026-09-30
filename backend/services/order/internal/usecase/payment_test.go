package usecase_test

import (
	"testing"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
)

func placedOrder(t *testing.T, f *checkoutFixture) *domain.Order {
	t.Helper()
	f.product("p1", "vendor-a", 100000)
	f.product("p2", "vendor-b", 50000)
	f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 1}, adapter.CartLine{ProductID: "p2", Quantity: 1})
	return f.checkout(t, "key-placed-order")
}

func TestMarkPaid_VerifiesTheCaptureAndPaysAllVendorOrdersTogether(t *testing.T) {
	f := newCheckoutFixture()
	order := placedOrder(t, f)
	f.pay(t, order, "11111111-1111-1111-1111-111111111111")

	paid := f.orders.get(order.ID)
	if paid.Status != domain.StatusPaid || paid.PaidAt == nil {
		t.Fatalf("expected paid, got %+v", paid)
	}
	vendorOrders, _ := f.vendorOrders.ListByOrderID(t.Context(), order.ID)
	for _, vo := range vendorOrders {
		if vo.Status != domain.StatusPaid {
			t.Errorf("vendor order %s not paid", vo.ID)
		}
		if _, ok := f.shipments.created[vo.ID]; !ok {
			t.Errorf("shipment of %s not created after payment", vo.ID)
		}
		in := f.shipments.created[vo.ID]
		if in.Quote == nil || in.Quote.FeeAmount != vo.ShippingFeeAmount {
			t.Errorf("shipment must carry the fee the buyer paid, got %+v", in.Quote)
		}
	}
	if !f.inventory.committedOrders[order.ID] {
		t.Fatal("stock must be committed before paid")
	}

	// A retried delivery of the same capture is a no-op.
	f.pay(t, order, "11111111-1111-1111-1111-111111111111")
	if len(f.payments.payments) != 1 {
		t.Fatalf("expected one capture record, got %d", len(f.payments.payments))
	}
}

func TestMarkPaid_RejectsAndRecordsCapturesThatCannotPayTheOrder(t *testing.T) {
	f := newCheckoutFixture()
	order := placedOrder(t, f)

	_, err := f.uc.MarkPaid(t.Context(), order.ID, &domain.PaymentCapture{PaymentID: "22222222-2222-2222-2222-222222222222", Amount: 1, Currency: "VND"})
	expectCode(t, err, apperror.CodeConflict)
	if o := f.orders.get(order.ID); o.Status != domain.StatusPendingPayment {
		t.Fatal("a wrong amount must not mark the order paid")
	}
	f.pay(t, order, "33333333-3333-3333-3333-333333333333")
	_, err = f.uc.MarkPaid(t.Context(), order.ID, &domain.PaymentCapture{PaymentID: "44444444-4444-4444-4444-444444444444", Amount: order.TotalAmount, Currency: "VND"})
	expectCode(t, err, apperror.CodeConflict)

	reasons := map[string]string{}
	for id, p := range f.payments.payments {
		if p.RejectionReason != nil {
			reasons[id] = *p.RejectionReason
		}
	}
	if reasons["22222222-2222-2222-2222-222222222222"] != domain.RejectAmountMismatch || reasons["44444444-4444-4444-4444-444444444444"] != domain.RejectDuplicatePayment {
		t.Fatalf("rejections must be recorded for refund review: %v", reasons)
	}
	exceptions, _ := f.uc.ListPaymentExceptions(t.Context(), 10, 0)
	if len(exceptions) != 2 {
		t.Fatalf("expected two payment exceptions, got %d", len(exceptions))
	}
}

func TestMarkPaid_LatePaymentOnCancelledOrderNeverResurrectsIt(t *testing.T) {
	f := newCheckoutFixture()
	order := placedOrder(t, f)
	if _, err := f.uc.MarkPaymentFailed(t.Context(), order.ID, "Stock reservation expired"); err != nil {
		t.Fatal(err)
	}
	_, err := f.uc.MarkPaid(t.Context(), order.ID, &domain.PaymentCapture{PaymentID: "55555555-5555-5555-5555-555555555555", Amount: order.TotalAmount, Currency: "VND"})
	expectCode(t, err, apperror.CodeConflict)
	if o := f.orders.get(order.ID); o.Status != domain.StatusCancelled {
		t.Fatalf("cancelled must never become paid, got %s", o.Status)
	}
	if f.inventory.committedOrders[order.ID] {
		t.Fatal("no stock may be committed for a cancelled order")
	}

	refund, err := f.uc.AdminRequestRefund(t.Context(), "admin-1", refundInput(order.ID, "", "55555555-5555-5555-5555-555555555555", domain.RefundReasonLatePayment, order.TotalAmount))
	if err != nil {
		t.Fatal(err)
	}
	if refund.Status != domain.RefundRequested || len(f.payment.requests) != 1 {
		t.Fatalf("the late capture must be submitted for refund, got %+v", refund)
	}
}

func TestMarkPaid_ExpiredHoldIsRejectedNotPaid(t *testing.T) {
	f := newCheckoutFixture()
	order := placedOrder(t, f)
	f.inventory.commitError = apperror.Conflict("Reservation deadline passed; payment requires reconciliation")

	_, err := f.uc.MarkPaid(t.Context(), order.ID, &domain.PaymentCapture{PaymentID: "66666666-6666-6666-6666-666666666666", Amount: order.TotalAmount, Currency: "VND"})
	expectCode(t, err, apperror.CodeConflict)
	if p := f.payments.payments["66666666-6666-6666-6666-666666666666"]; p == nil || *p.RejectionReason != domain.RejectStockNotHeld {
		t.Fatalf("expected a stock_not_held rejection, got %+v", p)
	}
}

func TestMarkPaid_LegacyOrderSnapshotsCommissionAtPaymentTime(t *testing.T) {
	f := newCheckoutFixture()
	plan, _ := domain.BuildCheckoutPlan("buyer-1", []domain.CheckoutLine{{ProductID: "p1", VendorID: "vendor-a", ProductName: "P", PriceAmount: 1000, Currency: "VND", Quantity: 1}})
	plan.Order.CheckoutState = domain.CheckoutReady
	order, _ := f.orders.CreateFromPlan(t.Context(), plan)

	if _, err := f.uc.MarkPaid(t.Context(), order.ID, nil); err != nil {
		t.Fatal(err)
	}
	vos, _ := f.vendorOrders.ListByOrderID(t.Context(), order.ID)
	if vos[0].Commission == nil || vos[0].Commission.Source != domain.CommissionSourceLegacy || *vos[0].CommissionAmount != 100 {
		t.Fatalf("expected a labelled legacy commission, got %+v", vos[0].Commission)
	}
}

func TestUpdateVendorOrderStatus_WeakestLinkAndOwnership(t *testing.T) {
	f := newCheckoutFixture()
	order := placedOrder(t, f)
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	f.vendors.approvedVendors["user-b"] = "vendor-b"
	vos, _ := f.vendorOrders.ListByOrderID(t.Context(), order.ID)
	voA, voB := vos[0], vos[1]
	if voA.VendorID != "vendor-a" {
		voA, voB = voB, voA
	}

	_, err := f.uc.UpdateVendorOrderStatus(t.Context(), "user-a", voA.ID, domain.StatusProcessing)
	expectCode(t, err, apperror.CodeConflict) // unpaid: fulfillment is closed
	f.pay(t, order, "77777777-7777-7777-7777-777777777777")

	_, err = f.uc.UpdateVendorOrderStatus(t.Context(), "user-a", voB.ID, domain.StatusProcessing)
	expectCode(t, err, apperror.CodeForbidden)

	for _, s := range []domain.Status{domain.StatusProcessing, domain.StatusShipped} {
		if _, err := f.uc.UpdateVendorOrderStatus(t.Context(), "user-a", voA.ID, s); err != nil {
			t.Fatal(err)
		}
	}
	if o := f.orders.get(order.ID); o.Status != domain.StatusPaid {
		t.Fatalf("the parent must wait for the slowest vendor, got %s", o.Status)
	}
	for _, s := range []domain.Status{domain.StatusProcessing, domain.StatusShipped} {
		if _, err := f.uc.UpdateVendorOrderStatus(t.Context(), "user-b", voB.ID, s); err != nil {
			t.Fatal(err)
		}
	}
	if o := f.orders.get(order.ID); o.Status != domain.StatusShipped {
		t.Fatalf("expected shipped once both shipped, got %s", o.Status)
	}
}

func TestAdminCancel_OnlyUnpaidAndReverifiesAdmin(t *testing.T) {
	f := newCheckoutFixture()
	order := placedOrder(t, f)
	f.identity.denied["admin-revoked"] = true

	_, err := f.uc.AdminCancel(t.Context(), "admin-revoked", order.ID, "fraud check")
	expectCode(t, err, apperror.CodeForbidden)
	_, err = f.uc.AdminCancel(t.Context(), "admin-1", order.ID, " ")
	expectCode(t, err, apperror.CodeValidation)

	f.pay(t, order, "88888888-8888-8888-8888-888888888888")
	_, err = f.uc.AdminCancel(t.Context(), "admin-1", order.ID, "customer request")
	expectCode(t, err, apperror.CodeConflict)
}
