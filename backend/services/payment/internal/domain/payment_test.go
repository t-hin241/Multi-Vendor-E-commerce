package domain

import (
	"testing"
	"time"
)

func TestCanTransition(t *testing.T) {
	cases := []struct {
		from, to Status
		want     bool
	}{
		{StatusCreating, StatusPending, true},
		{StatusCreating, StatusExpired, true},
		{StatusPending, StatusCaptured, true},
		{StatusPending, StatusAuthorized, true},
		{StatusPending, StatusFailed, true},
		{StatusPending, StatusExpired, true},
		{StatusAuthorized, StatusCaptured, true},
		{StatusCaptured, StatusRefunded, true},
		// Money that arrived late or out of order is still recorded.
		{StatusFailed, StatusCaptured, true},
		{StatusExpired, StatusCaptured, true},
		{StatusCaptured, StatusFailed, false},
		{StatusCaptured, StatusPending, false},
		{StatusRefunded, StatusCaptured, false},
		{StatusFailed, StatusPending, false},
		{StatusExpired, StatusPending, false},
		{StatusPending, StatusRefunded, false},
	}
	for _, tc := range cases {
		if got := CanTransition(tc.from, tc.to); got != tc.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestValidateAmount(t *testing.T) {
	if err := ValidateAmount(0); err == nil {
		t.Error("expected zero amount to be rejected")
	}
	if err := ValidateAmount(-1); err == nil {
		t.Error("expected negative amount to be rejected")
	}
	if err := ValidateAmount(1); err != nil {
		t.Errorf("expected positive amount to be accepted, got %v", err)
	}
}

func TestDecideReceipt(t *testing.T) {
	intent := func(s Status) *PaymentIntent { return &PaymentIntent{Amount: 5000, Currency: "VND", Status: s} }
	ok := &Receipt{EventType: EventSucceeded, Amount: 5000, Currency: "VND"}
	cases := []struct {
		name    string
		intent  *PaymentIntent
		receipt *Receipt
		status  Status
		rstatus ReceiptStatus
		outcome string
	}{
		{"capture", intent(StatusPending), ok, StatusCaptured, ReceiptProcessed, OutcomeApplied},
		{"capture while creating", intent(StatusCreating), ok, StatusCaptured, ReceiptProcessed, OutcomeApplied},
		{"late capture after expiry", intent(StatusExpired), ok, StatusCaptured, ReceiptProcessed, OutcomeApplied},
		{"out of order success after failure", intent(StatusFailed), ok, StatusCaptured, ReceiptProcessed, OutcomeApplied},
		{"duplicate", intent(StatusCaptured), ok, "", ReceiptProcessed, OutcomeDuplicate},
		{"amount mismatch", intent(StatusPending), &Receipt{EventType: EventSucceeded, Amount: 4000, Currency: "VND"}, "", ReceiptRejected, OutcomeAmountMismatch},
		{"currency mismatch", intent(StatusPending), &Receipt{EventType: EventSucceeded, Amount: 5000, Currency: "USD"}, "", ReceiptRejected, OutcomeAmountMismatch},
		{"failure", intent(StatusPending), &Receipt{EventType: EventFailed}, StatusFailed, ReceiptProcessed, OutcomeApplied},
		{"stale failure after capture", intent(StatusCaptured), &Receipt{EventType: EventFailed}, "", ReceiptProcessed, OutcomeStale},
		{"stale failure after expiry", intent(StatusExpired), &Receipt{EventType: EventFailed}, "", ReceiptProcessed, OutcomeStale},
		{"unknown event", intent(StatusPending), &Receipt{EventType: "payment.weird"}, "", ReceiptRejected, OutcomeUnsupported},
	}
	for _, tc := range cases {
		d := DecideReceipt(tc.intent, tc.receipt)
		if d.NewStatus != tc.status || d.ReceiptStatus != tc.rstatus || d.Outcome != tc.outcome {
			t.Errorf("%s: got %+v", tc.name, d)
		}
	}
}

func TestPayable(t *testing.T) {
	now := time.Now()
	past, future := now.Add(-time.Minute), now.Add(time.Minute)
	if !(&PaymentIntent{Status: StatusPending, ExpiresAt: &future}).Payable(now) {
		t.Error("an open link before expiry is payable")
	}
	if (&PaymentIntent{Status: StatusPending, ExpiresAt: &past}).Payable(now) || (&PaymentIntent{Status: StatusCreating}).Payable(now) {
		t.Error("expired or unconfirmed links are not payable")
	}
}
