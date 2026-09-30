package domain

import (
	"testing"
	"time"
)

func TestRefundCapCountsEveryNonFailedRefund(t *testing.T) {
	intent := &PaymentIntent{Amount: 1000, Currency: "VND", Status: StatusCaptured}
	req := RefundRequest{Amount: 400, Currency: "VND"}
	if err := CheckRefundable(intent, 600, req); err != nil {
		t.Fatalf("exactly the remaining amount must be refundable: %v", err)
	}
	if err := CheckRefundable(intent, 601, req); err == nil {
		t.Fatal("refunds must never exceed the capture")
	}
	if err := CheckRefundable(intent, 0, RefundRequest{Amount: 1, Currency: "USD"}); err == nil {
		t.Fatal("a refund in another currency must be refused")
	}
	for _, s := range []Status{StatusPending, StatusAuthorized, StatusFailed} {
		if err := CheckRefundable(&PaymentIntent{Amount: 1000, Currency: "VND", Status: s}, 0, req); err == nil {
			t.Fatalf("a %s payment has no money to refund", s)
		}
	}
}

func TestResolveNeedsProofAndNeverFlipsATerminalOutcome(t *testing.T) {
	now := time.Now()
	r := &Refund{Status: RefundAwaitingProvider}
	if _, err := r.Resolve(RefundResolution{Outcome: RefundSucceeded}, "admin", now); err == nil {
		t.Fatal("money must not count as returned without a reference")
	}
	if _, err := r.Resolve(RefundResolution{Outcome: RefundFailed}, "admin", now); err == nil {
		t.Fatal("a failure needs its reason")
	}
	changed, err := r.Resolve(RefundResolution{Outcome: RefundSucceeded, EvidenceReference: " FAKE-BANK-REF-1 "}, "admin", now)
	if err != nil || !changed || r.Status != RefundSucceeded || *r.EvidenceReference != "FAKE-BANK-REF-1" || *r.ResolvedBy != "admin" {
		t.Fatalf("unexpected resolution %+v %v", r, err)
	}
	if changed, err := r.Resolve(RefundResolution{Outcome: RefundSucceeded, EvidenceReference: "FAKE-BANK-REF-1"}, "admin", now); err != nil || changed {
		t.Fatal("repeating the same outcome must be a no-op")
	}
	if _, err := r.Resolve(RefundResolution{Outcome: RefundFailed, Note: "bounced"}, "admin", now); err == nil {
		t.Fatal("a succeeded refund must not become failed")
	}
}

func TestRefundRequestValidationAndReplayMatch(t *testing.T) {
	for _, bad := range []RefundRequest{
		{Amount: 0, Currency: "VND", Reason: "x"},
		{Amount: 1, Currency: "vnd", Reason: "x"},
		{Amount: 1, Currency: "VND", Reason: " "},
	} {
		if ValidateRefundRequest(bad) == nil {
			t.Fatalf("expected %+v to be invalid", bad)
		}
	}
	stored := &Refund{OrderID: "o", PaymentIntentID: "p", Amount: 10, Currency: "VND"}
	other := "q"
	if !stored.Same(RefundRequest{OrderID: "o", Amount: 10, Currency: "VND"}) || stored.Same(RefundRequest{OrderID: "o", Amount: 11, Currency: "VND"}) ||
		stored.Same(RefundRequest{OrderID: "o", Amount: 10, Currency: "VND", PaymentID: &other}) {
		t.Fatal("replay must match the stored refund exactly")
	}
}
