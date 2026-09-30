package usecase_test

import (
	"errors"
	"testing"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

func refundInput(orderID, vendorOrderID, paymentID, code string, amount int64) usecase.RefundInput {
	return usecase.RefundInput{OrderID: orderID, VendorOrderID: vendorOrderID, PaymentID: paymentID, ReasonCode: code, Amount: amount, Reason: "test refund"}
}

// deliveredOrder places, pays and completes an order for both vendors.
func deliveredOrder(t *testing.T, f *checkoutFixture) (*domain.Order, map[string]*domain.VendorOrder) {
	t.Helper()
	order := placedOrder(t, f)
	f.pay(t, order, "99999999-9999-9999-9999-999999999999")
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	f.vendors.approvedVendors["user-b"] = "vendor-b"
	byVendor := map[string]*domain.VendorOrder{}
	vos, _ := f.vendorOrders.ListByOrderID(t.Context(), order.ID)
	for _, vo := range vos {
		user := map[string]string{"vendor-a": "user-a", "vendor-b": "user-b"}[vo.VendorID]
		for _, s := range []domain.Status{domain.StatusProcessing, domain.StatusShipped, domain.StatusCompleted} {
			if _, err := f.uc.UpdateVendorOrderStatus(t.Context(), user, vo.ID, s); err != nil {
				t.Fatal(err)
			}
		}
		byVendor[vo.VendorID], _ = f.vendorOrders.FindByID(t.Context(), vo.ID)
	}
	return f.orders.get(order.ID), byVendor
}

func itemOf(f *checkoutFixture, vo *domain.VendorOrder) *domain.OrderItem {
	return f.vendorOrders.items[vo.ID][0]
}

func TestDisputeRefund_OnlyConfirmedRefundsChangeMoneyAndStatus(t *testing.T) {
	f := newCheckoutFixture()
	order, vos := deliveredOrder(t, f)
	voA := vos["vendor-a"]

	_, err := f.uc.AdminRequestRefund(t.Context(), "admin-1", refundInput(order.ID, voA.ID, "", domain.RefundReasonDispute, voA.Total()+1))
	expectCode(t, err, apperror.CodeConflict)

	refund, err := f.uc.AdminRequestRefund(t.Context(), "admin-1", refundInput(order.ID, voA.ID, "", domain.RefundReasonDispute, voA.Total()))
	if err != nil {
		t.Fatal(err)
	}
	if stored, _ := f.refunds.FindByID(t.Context(), refund.ID); stored.Status != domain.RefundSubmitted {
		t.Fatalf("expected the refund handed to Payment, got %s", stored.Status)
	}
	if o := f.orders.get(order.ID); o.RefundedAmount != 0 || o.Status != domain.StatusCompleted {
		t.Fatal("nothing is refunded before Payment confirms")
	}

	outcome := domain.RefundOutcome{RefundID: refund.ID, PaymentRefundID: "pr-1", Status: domain.RefundSucceeded, Amount: voA.Total(), Currency: "VND"}
	if err := f.uc.ApplyRefundOutcome(t.Context(), outcome); err != nil {
		t.Fatal(err)
	}
	if err := f.uc.ApplyRefundOutcome(t.Context(), outcome); err != nil {
		t.Fatalf("a replayed outcome must be accepted: %v", err)
	}
	outcome.Status = domain.RefundFailed
	expectCode(t, f.uc.ApplyRefundOutcome(t.Context(), outcome), apperror.CodeConflict)

	o := f.orders.get(order.ID)
	if o.RefundedAmount != voA.Total() || o.Status != domain.StatusCompleted {
		t.Fatalf("a partial refund must not refund the whole order, got %+v", o)
	}
	if vo, _ := f.vendorOrders.FindByID(t.Context(), voA.ID); vo.Status != domain.StatusRefunded || vo.RefundedAmount != voA.Total() {
		t.Fatalf("the fully refunded vendor order must be refunded, got %+v", vo)
	}
	if _, err := f.uc.AdminRequestRefund(t.Context(), "admin-1", refundInput(order.ID, voA.ID, "", domain.RefundReasonDispute, 1)); err == nil {
		t.Fatal("nothing is left to refund for this vendor order")
	}
}

func TestRefund_PaymentRefusalRejectsTheRefund(t *testing.T) {
	f := newCheckoutFixture()
	order, vos := deliveredOrder(t, f)
	f.payment.err = &apperror.Error{Code: apperror.CodeConflict, Status: 409, Message: "Refund exceeds the captured amount"}

	refund, err := f.uc.AdminRequestRefund(t.Context(), "admin-1", refundInput(order.ID, vos["vendor-b"].ID, "", domain.RefundReasonDispute, 1000))
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := f.refunds.FindByID(t.Context(), refund.ID)
	if stored.Status != domain.RefundRejected || stored.FailureReason == nil {
		t.Fatalf("expected rejected with Payment's reason, got %+v", stored)
	}
}

func TestRefund_PaymentOutageKeepsRetrying(t *testing.T) {
	f := newCheckoutFixture()
	order, vos := deliveredOrder(t, f)
	f.payment.err = apperror.Internal(errors.New("payment down"))
	refund, err := f.uc.AdminRequestRefund(t.Context(), "admin-1", refundInput(order.ID, vos["vendor-b"].ID, "", domain.RefundReasonDispute, 1000))
	if err != nil {
		t.Fatal(err)
	}
	if f.effects.count(domain.EffectRequestRefund, domain.EffectPending) != 1 {
		t.Fatal("the submission must stay queued")
	}
	f.payment.err = nil
	if _, err := f.uc.ProcessEffects(t.Context(), "", 20); err != nil {
		t.Fatal(err)
	}
	if stored, _ := f.refunds.FindByID(t.Context(), refund.ID); stored.Status != domain.RefundSubmitted {
		t.Fatalf("expected submitted after recovery, got %s", stored.Status)
	}
}

func TestReturnFlow_ApprovalReceiptRefundAndRestock(t *testing.T) {
	f := newCheckoutFixture()
	order, vos := deliveredOrder(t, f)
	item := itemOf(f, vos["vendor-a"])

	rr, err := f.uc.CreateReturn(t.Context(), "buyer-1", usecase.ReturnInput{OrderID: order.ID, ItemID: item.ID, Quantity: 1, Reason: "Broken", Evidence: "photo link"})
	if err != nil {
		t.Fatal(err)
	}
	if rr.RefundAmount != item.PriceAmount || rr.PolicyVersion != "window-7d" || *rr.ReturnWindowDays != 7 {
		t.Fatalf("unexpected return %+v", rr)
	}
	if _, err := f.uc.CreateReturn(t.Context(), "buyer-1", usecase.ReturnInput{OrderID: order.ID, ItemID: item.ID, Reason: "again"}); err == nil {
		t.Fatal("one return per item")
	}

	_, err = f.uc.VendorConfirmReturn(t.Context(), "user-b", rr.ID, "")
	expectCode(t, err, apperror.CodeForbidden)
	if _, err := f.uc.VendorConfirmReturn(t.Context(), "user-a", rr.ID, "ok"); err != nil {
		t.Fatal(err)
	}
	_, err = f.uc.MarkReturnReceived(t.Context(), "user-a", "vendor", rr.ID, true, "")
	expectCode(t, err, apperror.CodeConflict) // not approved yet
	_, err = f.uc.AdminDecideReturn(t.Context(), "admin-1", rr.ID, false, "")
	expectCode(t, err, apperror.CodeValidation) // rejection needs a reason
	if _, err := f.uc.AdminDecideReturn(t.Context(), "admin-1", rr.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	if stored, _ := f.returns.FindByID(t.Context(), rr.ID); stored.Status != domain.ReturnApproved {
		t.Fatal("approved must not look refunded")
	}

	received, err := f.uc.MarkReturnReceived(t.Context(), "user-a", "vendor", rr.ID, true, "box intact")
	if err != nil {
		t.Fatal(err)
	}
	if received.Status != domain.ReturnRefundPending || f.inventory.restocked[rr.ID] != 1 {
		t.Fatalf("expected refund pending and 1 unit restocked, got %s / %d", received.Status, f.inventory.restocked[rr.ID])
	}
	refund := f.refunds.only()
	if refund == nil || refund.Amount != item.PriceAmount || refund.ReturnRequestID == nil {
		t.Fatalf("expected a return refund, got %+v", refund)
	}

	if err := f.uc.ApplyRefundOutcome(t.Context(), domain.RefundOutcome{RefundID: refund.ID, Status: domain.RefundFailed, FailureReason: "bank rejected"}); err != nil {
		t.Fatal(err)
	}
	if stored, _ := f.returns.FindByID(t.Context(), rr.ID); stored.Status != domain.ReturnRefundFailed {
		t.Fatalf("expected refund_failed, got %s", stored.Status)
	}
	if _, err := f.uc.RetryReturnRefund(t.Context(), "admin-1", rr.ID); err != nil {
		t.Fatal(err)
	}
	var retry *domain.Refund
	for _, r := range f.refunds.refunds {
		if r.Status == domain.RefundSubmitted {
			retry = r
		}
	}
	if err := f.uc.ApplyRefundOutcome(t.Context(), domain.RefundOutcome{RefundID: retry.ID, Status: domain.RefundSucceeded, Amount: retry.Amount, Currency: "VND"}); err != nil {
		t.Fatal(err)
	}
	if stored, _ := f.returns.FindByID(t.Context(), rr.ID); stored.Status != domain.ReturnRefunded {
		t.Fatalf("expected refunded, got %s", stored.Status)
	}
	if o := f.orders.get(order.ID); o.RefundedAmount != item.PriceAmount || o.Status == domain.StatusRefunded {
		t.Fatalf("a one-item refund must not refund the whole order: %+v", o)
	}
	events, _ := f.uc.ReturnHistory(t.Context(), rr.ID)
	if len(events) < 7 {
		t.Fatalf("every step must be audited, got %d events", len(events))
	}
}

func TestReturnPolicy_WindowAndDeliveryAreEnforced(t *testing.T) {
	f := newCheckoutFixture()
	order := placedOrder(t, f)
	vos, _ := f.vendorOrders.ListByOrderID(t.Context(), order.ID)
	item := itemOf(f, vos[0])
	_, err := f.uc.CreateReturn(t.Context(), "buyer-1", usecase.ReturnInput{OrderID: order.ID, ItemID: item.ID, Reason: "not delivered yet"})
	expectCode(t, err, apperror.CodeConflict)

	f2 := newCheckoutFixture()
	order2, vos2 := deliveredOrder(t, f2)
	f2.now = time.Now().Add(8 * 24 * time.Hour)
	_, err = f2.uc.CreateReturn(t.Context(), "buyer-1", usecase.ReturnInput{OrderID: order2.ID, ItemID: itemOf(f2, vos2["vendor-a"]).ID, Reason: "late"})
	expectCode(t, err, apperror.CodeConflict)
	_, err = f2.uc.CreateReturn(t.Context(), "buyer-2", usecase.ReturnInput{OrderID: order2.ID, ItemID: itemOf(f2, vos2["vendor-a"]).ID, Reason: "not mine"})
	expectCode(t, err, apperror.CodeForbidden)
}

func TestVendorSummary_CountsConfirmedRefundsSeparately(t *testing.T) {
	f := newCheckoutFixture()
	order, vos := deliveredOrder(t, f)
	voA := vos["vendor-a"]
	refund, _ := f.uc.AdminRequestRefund(t.Context(), "admin-1", refundInput(order.ID, voA.ID, "", domain.RefundReasonDispute, 5000))
	summary, _, err := f.uc.GetVendorSummary(t.Context(), "user-a", "vendor-a")
	if err != nil || summary.TotalRefunded != 0 {
		t.Fatalf("an unconfirmed refund must not count: %+v %v", summary, err)
	}
	_ = f.uc.ApplyRefundOutcome(t.Context(), domain.RefundOutcome{RefundID: refund.ID, Status: domain.RefundSucceeded, Amount: 5000, Currency: "VND"})
	summary, _, _ = f.uc.GetVendorSummary(t.Context(), "user-a", "vendor-a")
	if summary.TotalRefunded != 5000 || summary.TotalRevenue != voA.SubtotalAmount {
		t.Fatalf("unexpected summary %+v", summary)
	}
}

func TestPartialReturns_QuantityIsCumulativeAndRejectedUnitsReturnable(t *testing.T) {
	f := newCheckoutFixture()
	order, vos := deliveredOrder(t, f)
	item := itemOf(f, vos["vendor-a"])
	item.Quantity = 3 // three units bought

	first, err := f.uc.CreateReturn(t.Context(), "buyer-1", usecase.ReturnInput{OrderID: order.ID, ItemID: item.ID, Quantity: 2, Reason: "two broken"})
	if err != nil || first.RefundAmount != 2*item.PriceAmount {
		t.Fatalf("partial return: %+v %v", first, err)
	}
	_, err = f.uc.CreateReturn(t.Context(), "buyer-1", usecase.ReturnInput{OrderID: order.ID, ItemID: item.ID, Quantity: 1, Reason: "one more"})
	expectCode(t, err, apperror.CodeConflict) // one open request per item

	if _, err := f.uc.AdminDecideReturn(t.Context(), "admin-1", first.ID, false, "no evidence"); err != nil {
		t.Fatal(err)
	}
	_, err = f.uc.CreateReturn(t.Context(), "buyer-1", usecase.ReturnInput{OrderID: order.ID, ItemID: item.ID, Quantity: 4, Reason: "too many"})
	expectCode(t, err, apperror.CodeValidation)
	second, err := f.uc.CreateReturn(t.Context(), "buyer-1", usecase.ReturnInput{OrderID: order.ID, ItemID: item.ID, Quantity: 3, Reason: "rejected units count again"})
	if err != nil || second.Quantity != 3 {
		t.Fatalf("rejected units must be returnable again: %+v %v", second, err)
	}
}

func TestCompletedVendorOrderIsReportedForSettlementOnce(t *testing.T) {
	f := newCheckoutFixture()
	_, vos := deliveredOrder(t, f)
	f.payment.mu.Lock()
	reports := append([]adapter.SettlementReport(nil), f.payment.settlements...)
	f.payment.mu.Unlock()
	if len(reports) != len(vos) {
		t.Fatalf("expected one settlement report per completed vendor order, got %d", len(reports))
	}
	for _, r := range reports {
		vo := vos[r.VendorID]
		if vo == nil || r.VendorOrderID != vo.ID || r.SubtotalAmount != vo.SubtotalAmount || r.ShippingAmount != vo.ShippingFeeAmount ||
			r.CommissionAmount != vo.Commission.Amount || r.CommissionRateBps != vo.Commission.RateBps {
			t.Fatalf("report does not carry the checkout snapshot: %+v", r)
		}
		if !r.EligibleAt.Equal(r.CompletedAt.Add(7 * 24 * time.Hour)) {
			t.Fatalf("sales become payable when the return window ends, got %v", r.EligibleAt.Sub(r.CompletedAt))
		}
	}
	if _, err := f.uc.ProcessEffects(t.Context(), "", 20); err != nil {
		t.Fatal(err)
	}
	if len(f.payment.settlements) != len(vos) {
		t.Fatal("a delivered report is not sent again")
	}
}
