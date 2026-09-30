package domain_test

import (
	"math"
	"testing"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
)

func line(vendor string, price, qty int64) domain.CheckoutLine {
	return domain.CheckoutLine{ProductID: "p-" + vendor, VendorID: vendor, ProductName: "P", PriceAmount: price, Currency: "VND", Quantity: qty}
}

func TestPlanSnapshotsShippingAndCommissionPerVendor(t *testing.T) {
	plan, err := domain.BuildCheckoutPlan("buyer", []domain.CheckoutLine{line("b", 50000, 1), line("a", 100000, 2)})
	if err != nil {
		t.Fatal(err)
	}
	quotes := map[string]domain.ShippingQuote{
		"a": {VendorID: "a", FeeAmount: 20000, Currency: "VND", FeeRuleID: "rule-a", FeeRuleVersion: 3},
		"b": {VendorID: "b", FeeAmount: 15000, Currency: "VND", FeeRuleID: "rule-b", FeeRuleVersion: 1},
	}
	if err := plan.ApplyShipping(quotes); err != nil {
		t.Fatal(err)
	}
	rule := &domain.CommissionRule{ID: "c1", Version: 4, RateBps: 1000}
	if err := plan.ApplyCommission(rule); err != nil {
		t.Fatal(err)
	}
	if plan.Order.SubtotalAmount != 250000 || plan.Order.ShippingAmount != 35000 || plan.Order.TotalAmount != 285000 {
		t.Fatalf("unexpected totals %+v", plan.Order)
	}
	var sum int64
	for _, vo := range plan.VendorOrders {
		sum += vo.Total()
		if vo.Commission == nil || *vo.Commission.RuleVersion != 4 || vo.Commission.BaseAmount != vo.SubtotalAmount ||
			vo.Commission.Amount+vo.Commission.NetAmount != vo.SubtotalAmount || vo.Commission.Rounding != "floor" {
			t.Errorf("bad commission snapshot %+v", vo.Commission)
		}
		if vo.Shipping == nil || vo.ShippingFeeAmount != quotes[vo.VendorID].FeeAmount {
			t.Errorf("bad shipping snapshot for %s", vo.VendorID)
		}
	}
	if sum != plan.Order.TotalAmount {
		t.Fatalf("parent total %d must equal the vendor totals %d", plan.Order.TotalAmount, sum)
	}
}

func TestPlanRejectsMissingOrForeignCurrencyQuotesAndOverflow(t *testing.T) {
	plan, _ := domain.BuildCheckoutPlan("buyer", []domain.CheckoutLine{line("a", 100, 1)})
	if err := plan.ApplyShipping(map[string]domain.ShippingQuote{}); err == nil {
		t.Error("a missing quote must never mean free shipping")
	}
	err := plan.ApplyShipping(map[string]domain.ShippingQuote{"a": {FeeAmount: 1, Currency: "USD"}})
	var appErr *apperror.Error
	if err == nil || !asAppError(err, &appErr) || appErr.Code != domain.CodeShippingUnavailable {
		t.Errorf("expected shipping_unavailable, got %v", err)
	}
	if _, err := domain.BuildCheckoutPlan("buyer", []domain.CheckoutLine{line("a", math.MaxInt64/2+1, 2)}); err == nil {
		t.Error("overflowing subtotal must be rejected")
	}
	if _, err := domain.SnapshotCommission(nil, 100); err == nil {
		t.Error("a missing commission rule must fail closed")
	}
	if _, _, ok := domain.ComputeCommission(math.MaxInt64, 10000); ok {
		t.Error("expected commission overflow to be reported")
	}
}

func TestCheckoutRequestHashAndKeys(t *testing.T) {
	v1, v2, total := int64(1), int64(2), int64(100)
	a := domain.CheckoutRequest{AddressID: "addr", CartVersion: &v1, ExpectedTotal: &total}
	b := domain.CheckoutRequest{AddressID: "addr", CartVersion: &v2, ExpectedTotal: &total}
	if a.Hash() == b.Hash() || a.Hash() != (domain.CheckoutRequest{AddressID: "addr", CartVersion: &v1, ExpectedTotal: &total}).Hash() {
		t.Error("hash must be stable and reflect the reviewed cart version")
	}
	for _, key := range []string{"short", string(make([]byte, 129)), "has space inside key"} {
		if domain.ValidateIdempotencyKey(key) == nil {
			t.Errorf("key %q must be rejected", key)
		}
	}
	if domain.ValidateIdempotencyKey("3f1c9a2e-8a8b-4c7e-9a41-1c2d3e4f5a6b") != nil {
		t.Error("a UUID key must be accepted")
	}
}

func TestJudgeCapture(t *testing.T) {
	ready := &domain.Order{Status: domain.StatusPendingPayment, CheckoutState: domain.CheckoutReady, TotalAmount: 1000, Currency: "VND"}
	ok := domain.PaymentCapture{PaymentID: "p1", Amount: 1000, Currency: "VND"}
	if r := domain.JudgeCapture(ready, ok, false); r != "" {
		t.Fatalf("expected the capture to apply, got %s", r)
	}
	cases := map[string]struct {
		order   domain.Order
		capture domain.PaymentCapture
		paid    bool
	}{
		domain.RejectAmountMismatch:   {*ready, domain.PaymentCapture{Amount: 999, Currency: "VND"}, false},
		domain.RejectOrderCancelled:   {domain.Order{Status: domain.StatusCancelled, CheckoutState: domain.CheckoutReady, TotalAmount: 1000, Currency: "VND"}, ok, false},
		domain.RejectDuplicatePayment: {*ready, ok, true},
		domain.RejectOrderNotReady:    {domain.Order{Status: domain.StatusPendingPayment, CheckoutState: domain.CheckoutPreparing, TotalAmount: 1000, Currency: "VND"}, ok, false},
	}
	for want, tc := range cases {
		order := tc.order
		if got := domain.JudgeCapture(&order, tc.capture, tc.paid); got != want {
			t.Errorf("expected %s, got %q", want, got)
		}
	}
}

func TestRefundOutcomeTransitions(t *testing.T) {
	if !domain.CanApplyOutcome(domain.RefundSubmitted, domain.RefundSucceeded) || !domain.CanApplyOutcome(domain.RefundSucceeded, domain.RefundSucceeded) {
		t.Error("valid outcome refused")
	}
	if domain.CanApplyOutcome(domain.RefundSucceeded, domain.RefundFailed) || domain.CanApplyOutcome(domain.RefundFailed, domain.RefundSucceeded) {
		t.Error("a terminal refund must not flip")
	}
	if _, err := domain.ValidateRefundRequest(101, 100, "too much"); err == nil {
		t.Error("refund over the refundable amount must be refused")
	}
}

func TestReturnPolicyAndTransitions(t *testing.T) {
	completed := time.Now().Add(-3 * 24 * time.Hour)
	policy := domain.ReturnPolicy{Version: "v1", WindowDays: 7}
	e := domain.ReturnEligibility{VendorOrderStatus: domain.StatusCompleted, CompletedAt: &completed, ItemQuantity: 3, ItemPrice: 1000, Now: time.Now()}
	amount, err := domain.ValidateNewReturn(policy, e, 2, "broken", nil)
	if err != nil || amount != 2000 {
		t.Fatalf("expected 2000, got %d %v", amount, err)
	}
	if _, err := domain.ValidateNewReturn(policy, e, 4, "broken", nil); err == nil {
		t.Error("more than purchased must be refused")
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	e.CompletedAt = &old
	if _, err := domain.ValidateNewReturn(policy, e, 1, "late", nil); err == nil {
		t.Error("outside the window must be refused")
	}
	e.VendorOrderStatus = domain.StatusShipped
	if _, err := domain.ValidateNewReturn(policy, e, 1, "not yet", nil); err == nil {
		t.Error("undelivered items must be refused")
	}
	if domain.CanTransitionReturn(domain.ReturnApproved, domain.ReturnRefunded) {
		t.Error("an approved return cannot be refunded before the goods are received")
	}
	if !domain.CanTransitionReturn(domain.ReturnRefundFailed, domain.ReturnRefundPending) {
		t.Error("a failed refund can be retried")
	}
}

func asAppError(err error, target **apperror.Error) bool {
	e, ok := err.(*apperror.Error)
	if ok {
		*target = e
	}
	return ok
}
