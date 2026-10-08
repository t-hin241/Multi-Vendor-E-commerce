package domain

import (
	"errors"
	"testing"
	"time"

	"shopee/backend/pkg/apperror"
)

func code(err error) apperror.Code {
	var app *apperror.Error
	if errors.As(err, &app) {
		return app.Code
	}
	return ""
}

func TestNormalizeBeneficiary(t *testing.T) {
	b, err := NormalizeBeneficiary(Beneficiary{BankCode: " vcb ", AccountNumber: "0123-4567 89", AccountName: "  nguyễn   văn  a "})
	if err != nil {
		t.Fatal(err)
	}
	if b.BankCode != "VCB" || b.AccountNumber != "0123456789" || b.AccountName != "NGUYỄN VĂN A" || b.Last4() != "6789" {
		t.Fatalf("unexpected normalisation %+v", b)
	}
	for _, bad := range []Beneficiary{
		{BankCode: "V", AccountNumber: "0123456789", AccountName: "A B"},
		{BankCode: "VCB", AccountNumber: "12345", AccountName: "A B"},
		{BankCode: "VCB", AccountNumber: "12345abc90", AccountName: "A B"},
		{BankCode: "VCB", AccountNumber: "0123456789", AccountName: "A"},
		{BankCode: "VCB", AccountNumber: "0123456789", AccountName: "<script>"},
	} {
		if _, err := NormalizeBeneficiary(bad); code(err) != apperror.CodeValidation {
			t.Errorf("%+v must be refused, got %v", bad, err)
		}
	}
}

func attempt(now time.Time) *ManualRefundAttempt {
	return &ManualRefundAttempt{ID: "a1", RefundID: "r1", Stage: AttemptReady, Version: 1, Amount: 100, Currency: "VND", CreatedAt: now.Add(-time.Hour)}
}

func TestAttemptLifecycle(t *testing.T) {
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	a := attempt(now)
	if err := a.Claim("op", now, 30*time.Minute); err != nil || a.Stage != AttemptExecuting {
		t.Fatalf("claim: %v %s", err, a.Stage)
	}
	if err := a.Claim("other", now, time.Minute); code(err) != "stale_attempt" {
		t.Fatalf("an executing attempt cannot be claimed again: %v", err)
	}
	sub := Submission{BankReference: "FT 2610-0001", SourceAccount: "PLATFORM-VCB", ExecutedAt: now}
	if err := a.Submit("other", sub, now); !errors.Is(err, ErrNotClaimOwner) {
		t.Fatalf("only the claimer submits an executing attempt: %v", err)
	}
	if err := a.Cancel("other", "nothing sent", now); !errors.Is(err, ErrNotClaimOwner) {
		t.Fatalf("only the claimer cancels an executing attempt: %v", err)
	}
	if err := a.Submit("op", Submission{BankReference: "FT1", SourceAccount: "X", ExecutedAt: now}, now); code(err) != apperror.CodeValidation {
		t.Fatalf("short source account must be refused: %v", err)
	}
	if err := a.Submit("op", Submission{BankReference: "FT26100001", SourceAccount: "PLATFORM-VCB", ExecutedAt: now.Add(time.Hour)}, now); code(err) != apperror.CodeValidation {
		t.Fatalf("future execution time must be refused: %v", err)
	}
	if err := a.Submit("op", sub, now); err != nil || a.Stage != AttemptSubmitted || *a.BankReferenceKey != "FT26100001" {
		t.Fatalf("submit: %v %+v", err, a)
	}
	if err := a.Decide("op", true, "ok", now, true); !errors.Is(err, ErrManualSelfApproval) {
		t.Fatalf("the executor cannot confirm under two-person review: %v", err)
	}
	if err := a.Decide("op", true, "ok", now, false); err != nil || a.Stage != AttemptConfirmed {
		t.Fatalf("single-operator mode lets the executor confirm: %v", err)
	}
	if a.EvidenceReference() != "bank:PLATFORM-VCB:FT 2610-0001" {
		t.Fatalf("evidence reference %q", a.EvidenceReference())
	}
	if err := a.Decide("rev", false, "no", now, true); code(err) != "stale_attempt" {
		t.Fatalf("a confirmed attempt is final: %v", err)
	}
}

func TestExpiredClaimIsUnknownAndOnlyFailsOrSubmits(t *testing.T) {
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	a := attempt(now)
	_ = a.Claim("op", now, 30*time.Minute)
	if a.LeaseExpired(now.Add(29 * time.Minute)) {
		t.Fatal("lease not yet expired")
	}
	if !a.LeaseExpired(now.Add(30 * time.Minute)) {
		t.Fatal("lease expired")
	}
	if err := a.Cancel("op", "nothing sent", now.Add(31*time.Minute)); code(err) != "lease_expired" {
		t.Fatalf("an expired claim cannot be cancelled as not sent: %v", err)
	}
	a.Stage = AttemptUnknown
	if err := a.Decide("rev", true, "found", now, true); code(err) != "stale_attempt" {
		t.Fatalf("unknown cannot be confirmed without a reference: %v", err)
	}
	if err := a.Cancel("op", "x", now); code(err) != "stale_attempt" {
		t.Fatalf("unknown cannot be voided: %v", err)
	}
	if !a.Stage.Active() {
		t.Fatal("unknown blocks another attempt")
	}
	if err := a.Submit("someone-else", Submission{BankReference: "FT26100002", SourceAccount: "PLATFORM-VCB", ExecutedAt: now}, now.Add(time.Hour)); err != nil {
		t.Fatalf("anyone who found the transfer on the statement submits an unknown attempt: %v", err)
	}
}

func TestStagesNeverPromiseMoneyBeforeConfirmation(t *testing.T) {
	open := &Refund{Status: RefundAwaitingProvider}
	pending := &RefundDestination{Status: DestinationPending}
	verified := &RefundDestination{Status: DestinationVerified}
	rejected := &RefundDestination{Status: DestinationRejected}
	cases := []struct {
		r            *Refund
		d            *RefundDestination
		a            *ManualRefundAttempt
		admin, buyer string
	}{
		{open, nil, nil, "awaiting_destination", "awaiting_destination"},
		{open, rejected, nil, "awaiting_destination", "destination_rejected"},
		{open, pending, nil, "verifying", "verifying"},
		{open, verified, nil, "ready", "processing"},
		{open, verified, &ManualRefundAttempt{Stage: AttemptSubmitted}, "submitted", "processing"},
		{open, verified, &ManualRefundAttempt{Stage: AttemptUnknown}, "unknown", "processing"},
		{&Refund{Status: RefundSucceeded}, verified, nil, "confirmed", "refunded"},
	}
	for _, c := range cases {
		if got := ManualStage(c.r, c.d, c.a); got != c.admin {
			t.Errorf("admin stage %s, want %s", got, c.admin)
		}
		if got := BuyerStage(c.r, c.d, c.a); got != c.buyer {
			t.Errorf("buyer stage %s, want %s", got, c.buyer)
		}
	}
}

func TestEvidenceContentTypeSniffsTheBytes(t *testing.T) {
	for data, want := range map[string]string{
		"\xFF\xD8\xFFrest":      "image/jpeg",
		"\x89PNG\r\n\x1a\nrest": "image/png",
		"%PDF-1.7 rest":         "application/pdf",
	} {
		if got, err := EvidenceContentType([]byte(data)); err != nil || got != want {
			t.Errorf("%q: %s %v", data[:4], got, err)
		}
	}
	if _, err := EvidenceContentType([]byte("<html>")); code(err) != "unsupported_file" {
		t.Errorf("html must be refused: %v", err)
	}
	if _, err := EvidenceContentType(make([]byte, MaxEvidenceBytes+1)); code(err) != "payload_too_large" {
		t.Errorf("oversized file must be refused: %v", err)
	}
}

func TestProofOperationsAreDistinct(t *testing.T) {
	refs := map[string]bool{
		DestinationDecisionRef("r", 1, true): true, DestinationDecisionRef("r", 1, false): true, DestinationDecisionRef("r", 2, true): true,
		DestinationRevealRef("r", 1): true, AttemptDecisionRef("a", 1, true): true, AttemptDecisionRef("a", 1, false): true,
	}
	if len(refs) != 6 {
		t.Fatal("each sensitive step needs its own proof operation")
	}
	if DestinationAAD("r", 1) == DestinationAAD("r", 2) {
		t.Fatal("AAD binds the version")
	}
}
