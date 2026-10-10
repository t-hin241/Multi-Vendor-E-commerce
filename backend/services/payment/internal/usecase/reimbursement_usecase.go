package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
)

// ReimbursementStore is repository.ReimbursementRepository.
type ReimbursementStore interface {
	OrderBuyer(ctx context.Context, orderID string) (string, error)
	Create(ctx context.Context, r *domain.Reimbursement) (bool, error)
	Lock(ctx context.Context, id string) (*domain.Reimbursement, error)
	Find(ctx context.Context, id string) (*domain.Reimbursement, error)
	Save(ctx context.Context, r *domain.Reimbursement, expected int) error
	VerifiedDestination(ctx context.Context, orderID, buyerID string) (string, string, error)
	BankReferenceUsed(ctx context.Context, key string) (bool, error)
	List(ctx context.Context, status string, limit, offset int) ([]*domain.Reimbursement, error)
}

// ReimbursementUseCase runs PW-032: the marketplace's own disbursements to
// buyers, outside any capture. Route permissions (AF-19) decide who may
// prepare (finance.prepare), approve (finance.approve) and record the
// transfer (finance.prepare); the use case keeps the approver distinct
// from the preparer and, with StepUp, asks for a password proof.
type ReimbursementUseCase struct {
	Store  ReimbursementStore
	Audit  AuditRepositoryPort
	Tx     Transactor
	Admins AdminAuthority
	// Enabled is FEATURE_REIMBURSEMENTS_ENABLED; MaxAmount is
	// PAYMENT_REIMBURSEMENT_MAX_AMOUNT (per reimbursement, minor units).
	Enabled   bool
	MaxAmount int64
	StepUp    bool
	Now       func() time.Time
	Log       zerolog.Logger
}

func (uc *ReimbursementUseCase) now() time.Time {
	if uc.Now != nil {
		return uc.Now().UTC()
	}
	return time.Now().UTC()
}

func reimbursementError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, repository.ErrReimbursementNotFound):
		return apperror.NotFound("Reimbursement not found")
	case errors.Is(err, repository.ErrStaleState):
		return domain.ErrReimbursementChanged
	case errors.Is(err, repository.ErrDuplicateBankReference):
		return domain.ErrDuplicateBankReference
	case errors.Is(err, repository.ErrDestinationNotFound):
		return domain.ErrNoVerifiedAccount
	}
	var app *apperror.Error
	if errors.As(err, &app) {
		return app
	}
	return apperror.Internal(err)
}

// Request prepares a reimbursement for the buyer of an order's capture.
func (uc *ReimbursementUseCase) Request(ctx context.Context, actor string, in domain.ReimbursementInput) (*domain.Reimbursement, bool, error) {
	if !uc.Enabled {
		return nil, false, domain.ErrReimbursementsOff
	}
	in, err := domain.ValidateReimbursement(in, uc.MaxAmount)
	if err != nil {
		return nil, false, err
	}
	var out *domain.Reimbursement
	created := false
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		buyer, err := uc.Store.OrderBuyer(ctx, in.OrderID)
		if errors.Is(err, repository.ErrReimbursementNotFound) {
			return apperror.Validation("The order has no captured payment to name its buyer")
		}
		if err != nil {
			return err
		}
		r := &domain.Reimbursement{OrderID: in.OrderID, BuyerID: buyer, ReasonCode: in.ReasonCode, Reason: in.Reason, Amount: in.Amount,
			Currency: in.Currency, RequestedBy: actor}
		if in.IdempotencyKey != "" {
			r.IdempotencyKey = &in.IdempotencyKey
		}
		if created, err = uc.Store.Create(ctx, r); err != nil {
			return err
		}
		out = r
		if !created {
			return nil
		}
		return uc.Audit.Record(ctx, actor, "reimbursement_requested", "reimbursement", r.ID,
			fmt.Sprintf("%d %s for order %s (%s): %s", r.Amount, r.Currency, r.OrderID, r.ReasonCode, r.Reason))
	})
	if err != nil {
		return nil, false, reimbursementError(err)
	}
	return out, created, nil
}

// ReimbursementDecision is a second admin's approval or rejection.
type ReimbursementDecision struct {
	Approve         bool
	Reason          string
	ExpectedVersion int
	Proof           string
}

// Decide approves or rejects a requested reimbursement.
func (uc *ReimbursementUseCase) Decide(ctx context.Context, actor, id string, in ReimbursementDecision) (*domain.Reimbursement, error) {
	reason, err := domain.ValidateReason(in.Reason)
	if err != nil {
		return nil, err
	}
	if uc.StepUp {
		if err := uc.Admins.ConsumeProof(ctx, in.Proof, actor, domain.ProofPurposeReimbursementDecide,
			domain.ReimbursementDecisionRef(id, in.ExpectedVersion, in.Approve)); err != nil {
			return nil, err
		}
	}
	var out *domain.Reimbursement
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		r, err := uc.Store.Lock(ctx, id)
		if err != nil {
			return err
		}
		if r.Version != in.ExpectedVersion {
			return domain.ErrReimbursementChanged
		}
		if err := r.Decide(actor, in.Approve, reason, uc.now()); err != nil {
			return err
		}
		if err := uc.Store.Save(ctx, r, in.ExpectedVersion); err != nil {
			return err
		}
		out = r
		return uc.Audit.Record(ctx, actor, "reimbursement_"+string(r.Status), "reimbursement", r.ID, reason)
	})
	if err != nil {
		return nil, reimbursementError(err)
	}
	uc.Log.Info().Str("reimbursement_id", id).Str("status", string(out.Status)).Str("actor_id", actor).Msg("payment_reimbursement_decided")
	return out, nil
}

// ReimbursementPayment is the bank transfer an admin made outside the app.
type ReimbursementPayment struct {
	BankReference   string
	ExpectedVersion int
}

// RecordPayment records the transfer of an approved reimbursement to the
// buyer's verified refund account of the order.
func (uc *ReimbursementUseCase) RecordPayment(ctx context.Context, actor, id string, in ReimbursementPayment) (*domain.Reimbursement, error) {
	var out *domain.Reimbursement
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		r, err := uc.Store.Lock(ctx, id)
		if err != nil {
			return err
		}
		if r.Version != in.ExpectedVersion {
			return domain.ErrReimbursementChanged
		}
		destination, masked, err := uc.Store.VerifiedDestination(ctx, r.OrderID, r.BuyerID)
		if err != nil {
			return err
		}
		if err := r.RecordPayment(actor, in.BankReference, destination, uc.now()); err != nil {
			return err
		}
		if used, err := uc.Store.BankReferenceUsed(ctx, *r.BankReferenceKey); err != nil {
			return err
		} else if used {
			return domain.ErrDuplicateBankReference
		}
		if err := uc.Store.Save(ctx, r, in.ExpectedVersion); err != nil {
			return err
		}
		r.DestinationMasked = &masked
		out = r
		return uc.Audit.Record(ctx, actor, "reimbursement_paid", "reimbursement", r.ID, fmt.Sprintf("bank %s to %s", *r.BankReference, masked))
	})
	if err != nil {
		return nil, reimbursementError(err)
	}
	uc.Log.Info().Str("reimbursement_id", id).Str("actor_id", actor).Msg("payment_reimbursement_paid")
	return out, nil
}

// List is the admin queue.
func (uc *ReimbursementUseCase) List(ctx context.Context, status string, limit, offset int) ([]*domain.Reimbursement, error) {
	switch status {
	case "", "requested", "approved", "paid", "rejected":
	default:
		return nil, apperror.Validation("status must be requested, approved, paid or rejected")
	}
	items, err := uc.Store.List(ctx, status, limit, offset)
	return items, reimbursementError(err)
}
