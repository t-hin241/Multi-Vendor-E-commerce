package usecase

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
)

// OrderSyncPort is the Order outcome outbox as admin sees it.
type OrderSyncPort interface {
	Requeue(ctx context.Context, intentID string) error
	ListProblems(ctx context.Context, limit int) ([]repository.SyncItem, error)
	Counts(ctx context.Context) (pending, review int64, err error)
}

// RefundSyncPort is the refund outcome outbox as admin sees it.
type RefundSyncPort interface {
	Requeue(ctx context.Context, refundID string) error
	ListProblems(ctx context.Context, limit int) ([]repository.RefundSyncItem, error)
}

type ReconciliationDeps struct {
	Tx         Transactor
	Payments   *PaymentUseCase
	Refunds    *RefundUseCase
	Intents    PaymentIntentRepositoryPort
	Receipts   ReceiptRepositoryPort
	OrderSync  OrderSyncPort
	RefundSync RefundSyncPort
	Audit      AuditRepositoryPort
	Roles      RoleVerifier
	Log        zerolog.Logger
}

// ReconciliationUseCase is admin's view of what did not reconcile between
// the provider, Payment and Order, with audited retries.
type ReconciliationUseCase struct{ ReconciliationDeps }

func NewReconciliationUseCase(d ReconciliationDeps) *ReconciliationUseCase {
	return &ReconciliationUseCase{d}
}

// Overview lists every open reconciliation problem.
type Overview struct {
	Counts            map[string]int64            `json:"counts"`
	ParkedReceipts    []*domain.Receipt           `json:"parked_receipts"`
	RejectedReceipts  []*domain.Receipt           `json:"rejected_receipts"`
	RetryableReceipts []*domain.Receipt           `json:"retryable_receipts"`
	OrderSync         []repository.SyncItem       `json:"order_sync"`
	RefundSync        []repository.RefundSyncItem `json:"refund_sync"`
	GeneratedAt       time.Time                   `json:"generated_at"`
}

func (uc *ReconciliationUseCase) requireAdmin(ctx context.Context, adminID string) error {
	if uc.Roles == nil {
		return apperror.Internal(errors.New("role verification is not configured"))
	}
	return asAppError(uc.Roles.RequireRole(ctx, adminID, "admin"))
}

// Report gathers the counters the worker logs and the admin screen shows.
func (uc *ReconciliationUseCase) Report(ctx context.Context) (map[string]int64, error) {
	counts, err := uc.Receipts.Counts(ctx)
	if err != nil {
		return nil, err
	}
	creating, expired, err := uc.Intents.Counts(ctx)
	if err != nil {
		return nil, err
	}
	pending, review, err := uc.OrderSync.Counts(ctx)
	if err != nil {
		return nil, err
	}
	counts["intents_stuck_creating"] = creating
	counts["intents_expired_open"] = expired
	counts["order_sync_pending"] = pending
	counts["order_sync_review"] = review
	return counts, nil
}

func (uc *ReconciliationUseCase) Overview(ctx context.Context, adminID string) (*Overview, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	out := &Overview{GeneratedAt: time.Now().UTC()}
	var err error
	if out.Counts, err = uc.Report(ctx); err != nil {
		return nil, asAppError(err)
	}
	if out.ParkedReceipts, err = uc.Receipts.ListByStatus(ctx, domain.ReceiptParked, 50, 0); err != nil {
		return nil, asAppError(err)
	}
	if out.RejectedReceipts, err = uc.Receipts.ListByStatus(ctx, domain.ReceiptRejected, 50, 0); err != nil {
		return nil, asAppError(err)
	}
	if out.RetryableReceipts, err = uc.Receipts.ListByStatus(ctx, domain.ReceiptRetryable, 50, 0); err != nil {
		return nil, asAppError(err)
	}
	if out.OrderSync, err = uc.OrderSync.ListProblems(ctx, 50); err != nil {
		return nil, asAppError(err)
	}
	if out.RefundSync, err = uc.RefundSync.ListProblems(ctx, 50); err != nil {
		return nil, asAppError(err)
	}
	return out, nil
}

// SearchResult is everything Payment knows under one reference.
type SearchResult struct {
	Intents  []*domain.PaymentIntent `json:"intents"`
	Receipts []*domain.Receipt       `json:"receipts"`
	Refunds  []*domain.Refund        `json:"refunds"`
}

// Search looks a reference up across intents, receipts and refunds: an
// order id, payment id, provider link id, provider reference or event id.
func (uc *ReconciliationUseCase) Search(ctx context.Context, adminID, q string) (*SearchResult, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	q = strings.TrimSpace(q)
	if len(q) < 3 || len(q) > 200 {
		return nil, apperror.Validation("Search text must be 3-200 characters")
	}
	out := &SearchResult{}
	var err error
	if out.Intents, err = uc.Intents.Search(ctx, q, 20); err != nil {
		return nil, asAppError(err)
	}
	if out.Receipts, err = uc.Receipts.Search(ctx, q, 50); err != nil {
		return nil, asAppError(err)
	}
	for _, i := range out.Intents {
		more, err := uc.Receipts.Search(ctx, i.ID, 50)
		if err != nil {
			return nil, asAppError(err)
		}
		out.Receipts = appendReceipts(out.Receipts, more)
	}
	if out.Refunds, err = uc.Refunds.Search(ctx, q, 20); err != nil {
		return nil, err
	}
	return out, nil
}

func appendReceipts(list, more []*domain.Receipt) []*domain.Receipt {
	seen := map[string]bool{}
	for _, r := range list {
		seen[r.ID] = true
	}
	for _, r := range more {
		if !seen[r.ID] {
			list = append(list, r)
			seen[r.ID] = true
		}
	}
	return list
}

func validReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) < 1 || len(reason) > 500 {
		return "", apperror.Validation("A reason of 1-500 characters is required")
	}
	return reason, nil
}

// RetryReceipt re-applies a parked or retryable receipt now.
func (uc *ReconciliationUseCase) RetryReceipt(ctx context.Context, adminID, receiptID, reason string) (*domain.Receipt, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	reason, err := validReason(reason)
	if err != nil {
		return nil, err
	}
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		if err := uc.Receipts.Requeue(ctx, receiptID); err != nil {
			return err
		}
		return uc.Audit.Record(ctx, adminID, "receipt_retry", "receipt", receiptID, reason)
	})
	if err != nil {
		return nil, asAppError(err)
	}
	if err := uc.Payments.applyReceipt(ctx, receiptID); err != nil {
		return nil, err
	}
	out, err := uc.Receipts.FindByID(ctx, receiptID)
	return out, asAppError(err)
}

// RetryOrderSync sends a captured/failed outcome to Order again after the
// cause of Order's refusal was dealt with.
func (uc *ReconciliationUseCase) RetryOrderSync(ctx context.Context, adminID, intentID, reason string) error {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return err
	}
	reason, err := validReason(reason)
	if err != nil {
		return err
	}
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		if err := uc.OrderSync.Requeue(ctx, intentID); err != nil {
			return err
		}
		return uc.Audit.Record(ctx, adminID, "order_sync_retry", "payment_intent", intentID, reason)
	})
	if err != nil {
		return asAppError(err)
	}
	if uc.Payments.SyncNow != nil {
		uc.Payments.SyncNow(ctx, intentID)
	}
	return nil
}

// RetryRefundSync sends a refund outcome to Order again.
func (uc *ReconciliationUseCase) RetryRefundSync(ctx context.Context, adminID, refundID, reason string) error {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return err
	}
	reason, err := validReason(reason)
	if err != nil {
		return err
	}
	return asAppError(uc.Tx.Run(ctx, func(ctx context.Context) error {
		if err := uc.RefundSync.Requeue(ctx, refundID); err != nil {
			return err
		}
		return uc.Audit.Record(ctx, adminID, "refund_sync_retry", "payment_refund", refundID, reason)
	}))
}

// ReconcileIntent asks the provider about one open intent now.
func (uc *ReconciliationUseCase) ReconcileIntent(ctx context.Context, adminID, intentID, reason string) (*domain.PaymentIntent, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	reason, err := validReason(reason)
	if err != nil {
		return nil, err
	}
	intent, err := uc.Intents.FindByID(ctx, intentID)
	if err != nil {
		return nil, asAppError(err)
	}
	if err := uc.Audit.Record(ctx, adminID, "intent_reconcile", "payment_intent", intentID, reason); err != nil {
		return nil, asAppError(err)
	}
	if intent.Status.Open() {
		if _, err := uc.Payments.resolveOpen(ctx, intent, false); err != nil {
			return nil, err
		}
	}
	out, err := uc.Intents.FindByID(ctx, intentID)
	return out, asAppError(err)
}

// History lists the audited admin actions on a target.
func (uc *ReconciliationUseCase) History(ctx context.Context, adminID, targetType, targetID string) ([]repository.AuditEntry, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	out, err := uc.Audit.ForTarget(ctx, targetType, targetID)
	return out, asAppError(err)
}
