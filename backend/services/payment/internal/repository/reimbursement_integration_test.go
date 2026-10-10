package repository_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
	"shopee/backend/services/payment/internal/usecase"
)

// PW-032: a reimbursement is prepared by one admin, approved by another
// (with a password proof) and paid to the buyer's verified refund account
// of the order; it is its own book (no refund row, nothing taken from the
// capture), a bank reference is used once, and without a verified account
// it waits.
func TestReimbursementTwoAdminsAndItsOwnBook(t *testing.T) {
	f := newManualFixture(t)
	ctx := t.Context()
	uc := &usecase.ReimbursementUseCase{Store: repository.ReimbursementRepository{Pool: f.e.pool}, Audit: repository.NewAuditRepository(f.e.pool),
		Tx: repository.Transactions{Pool: f.e.pool}, Admins: f.auth, Enabled: true, MaxAmount: 100_000, StepUp: true, Log: zerolog.Nop()}
	buyer := uuid.NewString()
	refund := f.refundFor(t, buyer, 1200)
	f.verified(t, buyer, refund.ID)
	refunds := f.e.count(t, `SELECT count(*) FROM payment_refunds WHERE order_id = $1`, refund.OrderID)

	in := domain.ReimbursementInput{OrderID: refund.OrderID, ReasonCode: "return_shipping_fee", Reason: "Phí gửi trả hàng do shop giao sai",
		Amount: 30_000, Currency: "VND", IdempotencyKey: "reimburse-key-1"}
	r, created, err := uc.Request(ctx, f.preparer, in)
	if err != nil || !created || r.Status != domain.ReimbursementRequested || r.BuyerID != buyer {
		t.Fatalf("request: %+v %v", r, err)
	}
	if again, created, err := uc.Request(ctx, f.preparer, in); err != nil || created || again.ID != r.ID {
		t.Fatalf("a repeat returns the same request: %+v %v", again, err)
	}
	big := in
	big.Amount, big.IdempotencyKey = 100_001, "reimburse-key-2"
	if _, _, err := uc.Request(ctx, f.preparer, big); err == nil {
		t.Fatal("above the limit is refused")
	}

	decide := func(actor string, version int) error {
		proof := f.auth.proof(actor, domain.ProofPurposeReimbursementDecide, domain.ReimbursementDecisionRef(r.ID, version, true))
		_, err := uc.Decide(ctx, actor, r.ID, usecase.ReimbursementDecision{Approve: true, Reason: "Đúng chứng từ", ExpectedVersion: version, Proof: proof})
		return err
	}
	if err := decide(f.preparer, r.Version); appCode(err) != "self_approval" {
		t.Fatalf("the preparer cannot approve: %v", err)
	}
	if err := decide(f.approverA, r.Version); err != nil {
		t.Fatal(err)
	}
	approved, _ := repository.ReimbursementRepository{Pool: f.e.pool}.Find(ctx, r.ID)
	paid, err := uc.RecordPayment(ctx, f.preparer2, r.ID, usecase.ReimbursementPayment{BankReference: "FAKE-FT-RB-0001", ExpectedVersion: approved.Version})
	if err != nil || paid.Status != domain.ReimbursementPaid || paid.DestinationMasked == nil || *paid.DestinationMasked != "FAKEBANK ••••2222" {
		t.Fatalf("paid to the verified account: %+v %v", paid, err)
	}
	if n := f.e.count(t, `SELECT count(*) FROM payment_refunds WHERE order_id = $1`, refund.OrderID); n != refunds {
		t.Fatal("a reimbursement is not a refund")
	}
	if n := f.e.count(t, `SELECT count(*) FROM payment_admin_audit WHERE target_type = 'reimbursement' AND target_id = $1`, r.ID); n != 3 {
		t.Fatalf("requested, approved and paid are audited, got %d", n)
	}

	other := f.refundFor(t, uuid.NewString(), 800)
	second, _, err := uc.Request(ctx, f.preparer, domain.ReimbursementInput{OrderID: other.OrderID, ReasonCode: "goodwill", Reason: "Xin lỗi",
		Amount: 10_000, Currency: "VND"})
	if err != nil {
		t.Fatal(err)
	}
	proof := f.auth.proof(f.approverB, domain.ProofPurposeReimbursementDecide, domain.ReimbursementDecisionRef(second.ID, second.Version, true))
	second, err = uc.Decide(ctx, f.approverB, second.ID, usecase.ReimbursementDecision{Approve: true, Reason: "ok", ExpectedVersion: second.Version, Proof: proof})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uc.RecordPayment(ctx, f.preparer, second.ID, usecase.ReimbursementPayment{BankReference: "FAKE-FT-RB-0002", ExpectedVersion: second.Version}); appCode(err) != "destination_not_verified" {
		t.Fatalf("no verified account, no payment: %v", err)
	}
	if _, err := f.e.pool.Exec(ctx, `DELETE FROM reimbursements WHERE id = $1`, r.ID); err == nil {
		t.Fatal("a reimbursement is never deleted")
	}
}
