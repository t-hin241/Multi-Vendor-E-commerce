package repository_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
	"shopee/backend/services/order/internal/usecase"
)

// holdPayment stands in for Payment: refunds are accepted, and the hold
// ledger answers per mode (ok, claimed: payout_already_claimed, down).
type holdPayment struct {
	mu       sync.Mutex
	mode     string
	acquired []adapter.HoldRequest
	released map[string]adapter.HoldRelease
}

func (p *holdPayment) RequestRefund(context.Context, adapter.RefundRequest) (*adapter.RefundReceipt, error) {
	return &adapter.RefundReceipt{PaymentRefundID: uuid.NewString(), Status: "awaiting_provider_refund"}, nil
}

func (p *holdPayment) SettleVendorOrder(context.Context, adapter.SettlementReport) error { return nil }

func (p *holdPayment) AcquireSettlementHold(_ context.Context, r adapter.HoldRequest) (*adapter.HoldReceipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.mode {
	case "down":
		return nil, apperror.Internal(errors.New("payment unreachable"))
	case "claimed":
		p.acquired = append(p.acquired, r)
		return nil, &apperror.Error{Code: "payout_already_claimed", Status: http.StatusConflict, Message: "A payout already claimed this vendor order"}
	}
	p.acquired = append(p.acquired, r)
	return &adapter.HoldReceipt{HoldID: r.HoldID, Status: "active"}, nil
}

func (p *holdPayment) ReleaseSettlementHold(_ context.Context, holdID string, r adapter.HoldRelease) (*adapter.HoldReceipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.released[holdID] = r
	return &adapter.HoldReceipt{HoldID: holdID, Status: "released"}, nil
}

func (p *holdPayment) setMode(mode string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mode = mode
}

func holdUseCase(pool *pgxpool.Pool, mode string) (*usecase.OrderUseCase, *holdPayment) {
	uc := supportUseCase(pool)
	payment := &holdPayment{mode: mode, released: map[string]adapter.HoldRelease{}}
	uc.Payment, uc.Holds, uc.CaseHolds = payment, payment, repository.SupportHoldRepository{Pool: pool}
	uc.Intakes = repository.SupportIntakeRepository{Pool: pool}
	uc.SupportConfig.HoldLedger = true
	return uc, payment
}

// runDue makes every pending effect due and runs them.
func runDue(t *testing.T, pool *pgxpool.Pool, uc *usecase.OrderUseCase) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE order_effects SET next_attempt_at = now() - interval '1 second' WHERE status = 'pending'`); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.ProcessEffects(t.Context(), "", 50); err != nil {
		t.Fatal(err)
	}
}

func holdOf(t *testing.T, pool *pgxpool.Pool, caseID string) *domain.CaseHold {
	t.Helper()
	h, err := repository.SupportHoldRepository{Pool: pool}.Get(t.Context(), caseID)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func code(err error) apperror.Code {
	var app *apperror.Error
	if errors.As(err, &app) {
		return app.Code
	}
	return ""
}

// PW-001 + PW-014: the case's hold is preparing until Payment confirms it;
// no refund opens from the case before that. The refund opens with its
// link in one step, idempotently, and closing the case releases the hold.
func TestSupportCaseHoldGatesTheRefundAndIsReleasedOnClose(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	uc, payment := holdUseCase(pool, "down")
	buyer, admin := uuid.NewString(), uuid.NewString()
	order, vo, vendor := paidSupportOrder(t, pool, buyer)

	sc, _, err := uc.CreateSupportCase(ctx, buyer, usecase.CreateSupportCaseInput{OrderID: order, VendorOrderID: vo, Category: "damaged", Message: "Hàng bị vỡ"})
	if err != nil {
		t.Fatal(err)
	}
	if h := holdOf(t, pool, sc.ID); h == nil || h.Status != domain.HoldPreparing {
		t.Fatalf("a money case starts with a preparing hold: %+v", h)
	}
	assigned, err := uc.AssignSupportCase(ctx, admin, sc.ID, admin, sc.Version, "Take it")
	if err != nil {
		t.Fatal(err)
	}
	refundIn := usecase.CaseRefundInput{Amount: 60, Reason: "Hàng vỡ, hoàn một phần", ExpectedVersion: assigned.Version, IdempotencyKey: "fake-case-refund-1"}
	if _, _, _, err := uc.CreateCaseRefund(ctx, admin, sc.ID, refundIn); code(err) != domain.CodeHoldUnavailable {
		t.Fatalf("no refund before Payment confirmed the hold: %v", err)
	}

	payment.setMode("ok")
	runDue(t, pool, uc)
	if h := holdOf(t, pool, sc.ID); h.Status != domain.HoldActive {
		t.Fatalf("hold confirmed by Payment: %+v", h)
	}
	if len(payment.acquired) != 1 || payment.acquired[0].VendorID != vendor || payment.acquired[0].VendorOrderID != vo ||
		payment.acquired[0].SourceType != "support_case" || payment.acquired[0].ReasonCode != "support_case_damaged" {
		t.Fatalf("acquire names the case and vendor order: %+v", payment.acquired)
	}

	pending, refund, replayed, err := uc.CreateCaseRefund(ctx, admin, sc.ID, refundIn)
	if err != nil || replayed || pending.Status != domain.CaseResolutionPending || *pending.ResolutionRef != refund.ID ||
		refund.ReasonCode != domain.RefundReasonDispute || *refund.VendorOrderID != vo {
		t.Fatalf("refund from case: %+v %+v %v %v", pending, refund, replayed, err)
	}
	again, sameRefund, replayed, err := uc.CreateCaseRefund(ctx, admin, sc.ID, refundIn)
	if err != nil || !replayed || sameRefund.ID != refund.ID || again.Version != pending.Version {
		t.Fatalf("a resend returns the same refund: %v %v", replayed, err)
	}
	changed := refundIn
	changed.Amount = 70
	if _, _, _, err := uc.CreateCaseRefund(ctx, admin, sc.ID, changed); code(err) != domain.CodeSupportKeyReused {
		t.Fatalf("the key cannot open another refund: %v", err)
	}
	var refunds int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM order_refunds WHERE order_id = $1`, order).Scan(&refunds); err != nil || refunds != 1 {
		t.Fatalf("exactly one refund: %d %v", refunds, err)
	}

	if err := uc.ApplyRefundOutcome(ctx, domain.RefundOutcome{RefundID: refund.ID, PaymentRefundID: uuid.NewString(), Status: domain.RefundSucceeded,
		Amount: 60, Currency: "VND"}); err != nil {
		t.Fatal(err)
	}
	detail, err := uc.GetSupportCase(ctx, usecase.SupportActor{ID: admin, Role: "admin"}, sc.ID)
	if err != nil || detail.Case.Status != domain.CaseResolved || detail.Hold == nil || detail.Hold.Status != domain.HoldActive {
		t.Fatalf("resolved once the money is back, hold kept until closed: %+v %v", detail, err)
	}
	buyerView, err := uc.GetSupportCase(ctx, usecase.SupportActor{ID: buyer, Role: "buyer"}, sc.ID)
	if err != nil || buyerView.Hold != nil {
		t.Fatalf("the buyer never sees the hold: %v", err)
	}
	for _, e := range buyerView.Events {
		if e.Action == "settlement_hold_active" {
			t.Fatal("hold events stay admin-only")
		}
	}
	if _, err := uc.CloseSupportCase(ctx, admin, sc.ID, detail.Case.Version, "Buyer received the refund"); err != nil {
		t.Fatal(err)
	}
	if h := holdOf(t, pool, sc.ID); h.Status != domain.HoldReleasing {
		t.Fatalf("closing queues the release: %+v", h)
	}
	runDue(t, pool, uc)
	h := holdOf(t, pool, sc.ID)
	if h.Status != domain.HoldReleased {
		t.Fatalf("released after Payment confirmed: %+v", h)
	}
	if r, ok := payment.released[h.HoldID]; !ok || r.OperationID != "support_case_closed:"+sc.ID || r.ResolutionRef != "refund:"+refund.ID {
		t.Fatalf("release names the case and its resolution: %+v", payment.released)
	}
}

// A payout that claimed the vendor order first leaves the case for review
// (never "protected"); the buyer's refund can still be opened.
func TestSupportCaseHoldAfterAPayoutClaimNeedsReview(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	uc, _ := holdUseCase(pool, "claimed")
	buyer, admin := uuid.NewString(), uuid.NewString()
	order, vo, _ := paidSupportOrder(t, pool, buyer)
	sc, _, err := uc.CreateSupportCase(ctx, buyer, usecase.CreateSupportCaseInput{OrderID: order, VendorOrderID: vo, Category: "missing_items", Message: "Thiếu hàng"})
	if err != nil {
		t.Fatal(err)
	}
	runDue(t, pool, uc)
	h := holdOf(t, pool, sc.ID)
	if h.Status != domain.HoldNeedsReview || h.Note == nil {
		t.Fatalf("payout already claimed → needs_review with Payment's reason: %+v", h)
	}
	assigned, err := uc.AssignSupportCase(ctx, admin, sc.ID, admin, sc.Version, "Take it")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := uc.CreateCaseRefund(ctx, admin, sc.ID, usecase.CaseRefundInput{Amount: 30, Reason: "Thiếu một món",
		ExpectedVersion: assigned.Version, IdempotencyKey: "fake-case-refund-2"}); err != nil {
		t.Fatalf("the buyer's refund is owed whatever happened to the payout: %v", err)
	}
}

// Cases opened before the ledger was on get their hold from the worker;
// a non-money case never has one.
func TestSupportHoldsAreBackfilledForOpenCases(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	uc, payment := holdUseCase(pool, "ok")
	uc.SupportConfig.HoldLedger = false
	buyer := uuid.NewString()
	order, vo, _ := paidSupportOrder(t, pool, buyer)
	money, _, err := uc.CreateSupportCase(ctx, buyer, usecase.CreateSupportCaseInput{OrderID: order, VendorOrderID: vo, Category: "not_received", Message: "Chưa nhận"})
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := uc.CreateSupportCase(ctx, buyer, usecase.CreateSupportCaseInput{OrderID: order, VendorOrderID: vo, Category: "other", Message: "Câu hỏi khác"})
	if err != nil {
		t.Fatal(err)
	}
	if holdOf(t, pool, money.ID) != nil {
		t.Fatal("no hold while the ledger is off")
	}
	uc.SupportConfig.HoldLedger = true
	if n, err := uc.EnsureSupportHolds(ctx, 10); err != nil || n != 1 {
		t.Fatalf("backfill: %d %v", n, err)
	}
	runDue(t, pool, uc)
	if h := holdOf(t, pool, money.ID); h == nil || h.Status != domain.HoldActive {
		t.Fatalf("backfilled hold is active: %+v", h)
	}
	if holdOf(t, pool, other.ID) != nil || len(payment.acquired) != 1 {
		t.Fatal("a case that cannot affect money has no hold")
	}
}

// PW-012: an intake only links to an order of the same buyer; it opens a
// case with the buyer's words, or joins the case already open there.
func TestSupportIntakeLinksOnlyTheBuyersOwnOrder(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	uc, _ := holdUseCase(pool, "ok")
	buyer, stranger, admin := uuid.NewString(), uuid.NewString(), uuid.NewString()
	order, vo, _ := paidSupportOrder(t, pool, buyer)
	otherOrder, otherVO, _ := paidSupportOrder(t, pool, stranger)

	intake, _, err := uc.CreateSupportIntake(ctx, buyer, usecase.SupportIntakeInput{ReferenceKind: "bank_transfer", Reference: "FAKE FT 0001",
		Message: "Tôi đã chuyển khoản nhưng không thấy đơn"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := uc.CreateSupportIntake(ctx, buyer, usecase.SupportIntakeInput{ReferenceKind: "bank_transfer", Reference: "fake ft 0001",
		Message: "Gửi lại"}); code(err) != domain.CodeIntakeAlreadyOpen {
		t.Fatalf("one open intake per reference: %v", err)
	}
	link := usecase.LinkIntakeInput{OrderID: otherOrder, VendorOrderID: otherVO, Category: "payment_issue", ExpectedVersion: intake.Version, Reason: "Matched by amount"}
	if _, err := uc.LinkSupportIntake(ctx, admin, intake.ID, link); code(err) != domain.CodeIntakeOrderMismatch {
		t.Fatalf("another buyer's order must be refused: %v", err)
	}
	link.OrderID, link.VendorOrderID, link.Reason = order, vo, "Matched by bank reference"
	sc, err := uc.LinkSupportIntake(ctx, admin, intake.ID, link)
	if err != nil || sc.BuyerID != buyer || sc.OrderID != order || sc.Category != domain.CategoryPaymentIssue {
		t.Fatalf("link opens the buyer's case: %+v %v", sc, err)
	}
	detail, err := uc.GetSupportCase(ctx, usecase.SupportActor{ID: buyer, Role: "buyer"}, sc.ID)
	if err != nil || len(detail.Messages) != 1 || detail.Messages[0].Text != "Tôi đã chuyển khoản nhưng không thấy đơn" || detail.Messages[0].AuthorRole != "buyer" {
		t.Fatalf("the case carries the buyer's words: %+v %v", detail, err)
	}
	if _, err := uc.LinkSupportIntake(ctx, admin, intake.ID, link); code(err) != domain.CodeVersionConflict {
		t.Fatalf("a linked intake is final: %v", err)
	}

	second, _, err := uc.CreateSupportIntake(ctx, buyer, usecase.SupportIntakeInput{ReferenceKind: "payment", Reference: "FAKE-PAY-2", Message: "Thêm thông tin"})
	if err != nil {
		t.Fatal(err)
	}
	link.ExpectedVersion = second.Version
	joined, err := uc.LinkSupportIntake(ctx, admin, second.ID, link)
	if err != nil || joined.ID != sc.ID {
		t.Fatalf("a second intake joins the open case: %+v %v", joined, err)
	}

	for i, ref := range []string{"FAKE-REF-A", "FAKE-REF-B", "FAKE-REF-C"} {
		_, _, err := uc.CreateSupportIntake(ctx, buyer, usecase.SupportIntakeInput{ReferenceKind: "checkout", Reference: ref, Message: "x"})
		if i < 3 && err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := uc.CreateSupportIntake(ctx, buyer, usecase.SupportIntakeInput{ReferenceKind: "checkout", Reference: "FAKE-REF-D", Message: "x"}); code(err) != domain.CodeTooManyIntakes {
		t.Fatalf("at most 3 open intakes: %v", err)
	}
	open, err := uc.ListSupportIntakes(ctx, domain.IntakeOpen, 10, 0)
	if err != nil || len(open) != 3 {
		t.Fatalf("admin queue: %d %v", len(open), err)
	}
	closed, err := uc.CloseSupportIntake(ctx, admin, open[0].ID, open[0].Version, "No payment matches this reference")
	if err != nil || closed.Status != domain.IntakeClosed || *closed.CloseReason != "No payment matches this reference" {
		t.Fatalf("close: %+v %v", closed, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE support_intakes SET message = 'edited' WHERE id = $1`, intake.ID); err == nil {
		t.Fatal("a handled intake is final")
	}
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM order_admin_audit WHERE entity_type = 'support_intake'`).Scan(&audits); err != nil || audits != 3 {
		t.Fatalf("two links and a close are audited: %d %v", audits, err)
	}
}
