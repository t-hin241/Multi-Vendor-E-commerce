package repository_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/adapter"
	"shopee/backend/services/payment/internal/domain"
)

// stableVendors gives each vendor one verified account, as Vendor would.
type stableVendors struct {
	mu       sync.Mutex
	accounts map[string]string
}

func (f *stableVendors) DefaultDestination(_ context.Context, vendorID string) (*adapter.PayoutDestination, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.accounts[vendorID] == "" {
		f.accounts[vendorID] = uuid.NewString()
	}
	return &adapter.PayoutDestination{AccountID: f.accounts[vendorID], Version: 1, BankBIN: "970400", Last4: "1234"}, nil
}

func expectAppCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	var app *apperror.Error
	if !errors.As(err, &app) || app.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

// PW-027: one admin creates a payout batch and another approves it before
// any transfer result is recorded (the database refuses one too). Paying
// a destination for the first time needs a second, distinct approver. A
// rejected or withdrawn batch cancels its items and the next batch pays
// them.
func TestPayoutBatchNeedsAnIndependentApprover(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.settle.Vendors = &stableVendors{accounts: map[string]string{}}
	e.settle.BatchApproval = true
	e.settle.ApprovalPolicy = domain.PayoutApprovalPolicy{Currency: "VND", BatchLimit: 10_000_000, ItemLimit: 5_000_000}
	maker, first, second, vendor := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	old := time.Now().Add(-30 * 24 * time.Hour)
	if _, err := e.settle.IngestVendorOrder(ctx, settlementOrder(vendor, old)); err != nil {
		t.Fatal(err)
	}

	// A destination never paid before: two approvers.
	b, _, err := e.settle.CreatePayoutBatch(ctx, maker, "fake-approval-batch-1", "VND", nil)
	if err != nil || b.Approval.State != domain.PayoutAwaitingApproval || b.Approval.RequiredApprovals != 2 ||
		len(b.Approval.RiskReasons) != 1 || b.Approval.RiskReasons[0] != domain.PayoutRiskNewDestination || b.TotalAmount != 110000 {
		t.Fatalf("new batch: %+v %v", b, err)
	}
	item := b.Items[0]
	paid := domain.PayoutResolution{Outcome: domain.PayoutItemSucceeded, EvidenceReference: "FAKE-BANK-REF-1"}
	_, err = e.settle.ResolvePayoutItem(ctx, maker, item.ID, paid)
	expectAppCode(t, err, "payout_batch_not_approved")
	_, err = e.settle.ApprovePayoutBatch(ctx, maker, b.ID, domain.PayoutAwaitingApproval, "")
	expectAppCode(t, err, "self_approval")
	if b, err = e.settle.ApprovePayoutBatch(ctx, first, b.ID, domain.PayoutAwaitingApproval, ""); err != nil || b.Approval.State != domain.PayoutAwaitingSecondApproval {
		t.Fatalf("first approval: %+v %v", b, err)
	}
	_, err = e.settle.ResolvePayoutItem(ctx, maker, item.ID, paid)
	expectAppCode(t, err, "payout_batch_not_approved")
	_, err = e.settle.ApprovePayoutBatch(ctx, first, b.ID, domain.PayoutAwaitingSecondApproval, "")
	expectAppCode(t, err, "same_approver")
	_, err = e.settle.ApprovePayoutBatch(ctx, second, b.ID, domain.PayoutAwaitingApproval, "")
	expectAppCode(t, err, "approval_changed")
	if b, err = e.settle.ApprovePayoutBatch(ctx, second, b.ID, domain.PayoutAwaitingSecondApproval, ""); err != nil || b.Approval.State != domain.PayoutApproved {
		t.Fatalf("second approval: %+v %v", b, err)
	}
	if _, err := e.settle.ResolvePayoutItem(ctx, maker, item.ID, paid); err != nil {
		t.Fatalf("an approved batch records its transfer: %v", err)
	}

	// The same destination again, under the limits: one approver.
	if _, err := e.settle.IngestVendorOrder(ctx, settlementOrder(vendor, old)); err != nil {
		t.Fatal(err)
	}
	b2, _, err := e.settle.CreatePayoutBatch(ctx, maker, "fake-approval-batch-2", "VND", nil)
	if err != nil || b2.Approval.RequiredApprovals != 1 || len(b2.Approval.RiskReasons) != 0 {
		t.Fatalf("known destination: %+v %v", b2, err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE payout_items SET status = 'succeeded', evidence_reference = 'FAKE-SQL' WHERE id = $1`, b2.Items[0].ID); err == nil {
		t.Fatal("the database refuses a transfer result on an unapproved batch")
	}
	_, err = e.settle.ClosePayoutBatch(ctx, maker, b2.ID, "Sai kỳ", false)
	expectAppCode(t, err, apperror.CodeConflict)
	if b2, err = e.settle.ClosePayoutBatch(ctx, first, b2.ID, "Số liệu kỳ này chưa đối soát", false); err != nil ||
		b2.Approval.State != domain.PayoutRejected || b2.Items[0].Status != domain.PayoutItemCancelled {
		t.Fatalf("rejected: %+v %v", b2, err)
	}
	_, err = e.settle.ApprovePayoutBatch(ctx, second, b2.ID, domain.PayoutAwaitingApproval, "")
	expectAppCode(t, err, "approval_closed")

	// The cancelled item's entries are paid by the next batch; its creator
	// may withdraw it, nobody else.
	b3, _, err := e.settle.CreatePayoutBatch(ctx, maker, "fake-approval-batch-3", "VND", nil)
	if err != nil || len(b3.Items) != 1 || b3.Items[0].Amount != 110000 {
		t.Fatalf("released entries in the next batch: %+v %v", b3, err)
	}
	_, err = e.settle.ClosePayoutBatch(ctx, first, b3.ID, "Không phải của tôi", true)
	expectAppCode(t, err, apperror.CodeForbidden)
	if b3, err = e.settle.ClosePayoutBatch(ctx, maker, b3.ID, "Tạo nhầm", true); err != nil || b3.Approval.State != domain.PayoutWithdrawn {
		t.Fatalf("withdrawn: %+v %v", b3, err)
	}

	for action, want := range map[string]int{"payout_batch_first_approval": 1, "payout_batch_approved": 1, "payout_batch_rejected": 1, "payout_batch_withdrawn": 1} {
		if n := e.count(t, `SELECT count(*) FROM payment_admin_audit WHERE action = $1`, action); n != want {
			t.Errorf("%s audited %d times, want %d", action, n, want)
		}
	}
}
