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

// RefundUseCase returns money Order decided to refund. Order owns the
// decision; Payment owns the cap against the capture and the proof that
// money actually left. With no automated provider refund yet, an operator
// resolves each refund with the provider or bank reference.
type RefundUseCase struct {
	refunds RefundRepositoryPort
	roles   RoleVerifier
	log     zerolog.Logger
	now     func() time.Time
	tx      Transactor
	// settle posts a succeeded refund to the vendor's settlement ledger in
	// the refund's transaction.
	settle func(ctx context.Context, refund *domain.Refund) error
	// approvals (AF-19): a resolution only goes through an approved
	// maker-checker request, never directly.
	approvals bool
	// legacyGuard (AF-06) refuses the one-step resolution while a manual
	// transfer is open or the manual workflow is on; it runs on the locked
	// refund in the resolution's transaction.
	legacyGuard func(ctx context.Context, r *domain.Refund, res domain.RefundResolution) error
}

// WithLegacyGuard installs the manual workflow's check on the one-step
// resolution (direct and through an approval).
func (uc *RefundUseCase) WithLegacyGuard(guard func(ctx context.Context, r *domain.Refund, res domain.RefundResolution) error) *RefundUseCase {
	uc.legacyGuard = guard
	return uc
}

// legacy wraps check with the legacy guard.
func (uc *RefundUseCase) legacy(res domain.RefundResolution, check func(context.Context, *domain.Refund) error) func(context.Context, *domain.Refund) error {
	return func(ctx context.Context, r *domain.Refund) error {
		if uc.legacyGuard != nil {
			if err := uc.legacyGuard(ctx, r, res); err != nil {
				return err
			}
		}
		if check != nil {
			return check(ctx, r)
		}
		return nil
	}
}

// RequireApprovals turns the direct resolution off: it then needs an
// approved request (ApprovalUseCase).
func (uc *RefundUseCase) RequireApprovals(on bool) *RefundUseCase {
	uc.approvals = on
	return uc
}

// ErrApprovalRequired: the action needs a maker-checker request.
var ErrApprovalRequired = &apperror.Error{Code: "approval_required", Status: 409,
	Message: "This action needs a second admin: create an approval request instead"}

func NewRefundUseCase(refunds RefundRepositoryPort, roles RoleVerifier, log zerolog.Logger) *RefundUseCase {
	return &RefundUseCase{refunds: refunds, roles: roles, log: log, now: time.Now}
}

// WithSettlement makes resolved refunds post to the settlement ledger.
func (uc *RefundUseCase) WithSettlement(tx Transactor, settlement *SettlementUseCase) *RefundUseCase {
	uc.tx, uc.settle = tx, settlement.PostRefund
	return uc
}

// Request accepts Order's refund request idempotently by its refund id.
// created is false for a replay of an already accepted request.
func (uc *RefundUseCase) Request(ctx context.Context, req domain.RefundRequest) (*domain.Refund, bool, error) {
	req.Currency = strings.ToUpper(strings.TrimSpace(req.Currency))
	req.Reason = strings.TrimSpace(req.Reason)
	if err := domain.ValidateRefundRequest(req); err != nil {
		return nil, false, err
	}
	refund, created, err := uc.refunds.Request(ctx, req, func(intent *domain.PaymentIntent, committed int64) error {
		return domain.CheckRefundable(intent, committed, req)
	})
	if err != nil {
		return nil, false, asAppError(err)
	}
	if created {
		uc.log.Info().Str("payment_refund_id", refund.ID).Str("order_refund_id", req.OrderRefundID).
			Str("order_id", refund.OrderID).Str("payment_intent_id", refund.PaymentIntentID).Int64("amount", refund.Amount).
			Msg("payment_refund_requested")
	}
	return refund, created, nil
}

func (uc *RefundUseCase) GetAdmin(ctx context.Context, adminID, id string) (*domain.Refund, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	r, err := uc.refunds.FindByID(ctx, id)
	if errors.Is(err, repository.ErrRefundNotFound) {
		return nil, apperror.NotFound("Refund not found")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return r, nil
}

func (uc *RefundUseCase) List(ctx context.Context, adminID, status string, limit, offset int) ([]*domain.Refund, int, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, 0, err
	}
	switch domain.RefundStatus(status) {
	case "", "open", domain.RefundAwaitingProvider, domain.RefundPending, domain.RefundSucceeded, domain.RefundFailed:
	default:
		return nil, 0, apperror.Validation("Unknown refund status filter")
	}
	refunds, total, err := uc.refunds.List(ctx, status, limit, offset)
	if err != nil {
		return nil, 0, apperror.Internal(err)
	}
	return refunds, total, nil
}

// Resolve records what happened to the money. The outcome is queued for
// Order in the same transaction.
func (uc *RefundUseCase) Resolve(ctx context.Context, adminID, refundID string, res domain.RefundResolution) (*domain.Refund, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	if uc.approvals {
		return nil, ErrApprovalRequired
	}
	return uc.resolve(ctx, adminID, refundID, res, uc.legacy(res, nil))
}

// resolve applies a resolution in the caller's transaction (or its own);
// check, when set, verifies the locked refund first (an approval's
// snapshot).
func (uc *RefundUseCase) resolve(ctx context.Context, adminID, refundID string, res domain.RefundResolution, check func(context.Context, *domain.Refund) error) (*domain.Refund, error) {
	var refund *domain.Refund
	resolve := func(ctx context.Context) error {
		var err error
		refund, err = uc.refunds.Resolve(ctx, refundID, func(r *domain.Refund) (bool, error) {
			if check != nil {
				if err := check(ctx, r); err != nil {
					return false, err
				}
			}
			return r.Resolve(res, adminID, uc.now().UTC())
		})
		if err != nil || uc.settle == nil {
			return err
		}
		return uc.settle(ctx, refund)
	}
	var err error
	if uc.tx != nil {
		err = uc.tx.Run(ctx, resolve)
	} else {
		err = resolve(ctx)
	}
	if err != nil {
		return nil, asAppError(err)
	}
	uc.log.Info().Str("payment_refund_id", refund.ID).Str("order_id", refund.OrderID).Str("status", string(refund.Status)).
		Str("actor_id", adminID).Msg("payment_refund_resolved")
	return refund, nil
}

func (uc *RefundUseCase) requireAdmin(ctx context.Context, adminID string) error {
	if uc.roles == nil {
		return apperror.Internal(errors.New("role verification is not configured"))
	}
	if err := uc.roles.RequireRole(ctx, adminID, "admin"); err != nil {
		return asAppError(err)
	}
	return nil
}

func asAppError(err error) error {
	if err == nil {
		return nil
	}
	var app *apperror.Error
	if errors.As(err, &app) {
		return app
	}
	if errors.Is(err, repository.ErrRefundNotFound) {
		return apperror.NotFound("Refund not found")
	}
	switch {
	case errors.Is(err, repository.ErrPaymentIntentNotFound):
		return apperror.NotFound("Payment not found")
	case errors.Is(err, repository.ErrPayoutItemNotFound):
		return apperror.NotFound("Payout item not found")
	case errors.Is(err, repository.ErrPayoutBatchNotFound):
		return apperror.NotFound("Payout batch not found")
	case errors.Is(err, repository.ErrReceiptNotFound):
		return apperror.NotFound("Receipt not found")
	case errors.Is(err, repository.ErrStaleState):
		return apperror.Conflict("This record changed meanwhile; reload and try again")
	}
	return apperror.Internal(err)
}

// Search finds refunds for the admin reconciliation view.
func (uc *RefundUseCase) Search(ctx context.Context, q string, limit int) ([]*domain.Refund, error) {
	out, err := uc.refunds.Search(ctx, q, limit)
	return out, asAppError(err)
}
