package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
)

// PW-027: a payout batch is created by one admin and approved by another;
// a batch over the limits, or paying a destination for the first time,
// needs a second, distinct approver. Route permissions (AF-19) give
// creation to finance.prepare and approval to finance.approve; the use
// case keeps the people distinct whatever the permissions.

// requireBatchApproval records what a new batch needs, in its transaction,
// and returns the note for the creation audit.
func (uc *SettlementUseCase) requireBatchApproval(ctx context.Context, batchID, currency string) (string, error) {
	b, err := uc.Payouts.FindBatch(ctx, batchID)
	if err != nil {
		return "", err
	}
	fresh, err := uc.Payouts.NewDestinations(ctx, batchID)
	if err != nil {
		return "", err
	}
	required, reasons := uc.ApprovalPolicy.Assess(currency, b.Items, fresh)
	var total int64
	for _, it := range b.Items {
		total += it.Amount
	}
	if err := uc.Payouts.SetApprovalRequirement(ctx, batchID, total, required, reasons); err != nil {
		return "", err
	}
	note := fmt.Sprintf("; needs %d approval(s)", required)
	if len(reasons) > 0 {
		note += " (" + strings.Join(reasons, ", ") + ")"
	}
	return note, nil
}

func payoutApprovalError(err error) error {
	if errors.Is(err, repository.ErrStaleState) {
		return domain.ErrPayoutApprovalChanged
	}
	return asAppError(err)
}

// ApprovePayoutBatch records actor's approval of the stage they reviewed
// (expectedState: awaiting_approval or awaiting_second_approval).
func (uc *SettlementUseCase) ApprovePayoutBatch(ctx context.Context, adminID, batchID, expectedState, proof string) (*domain.PayoutBatch, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	if uc.StepUp {
		if uc.Admins == nil {
			return nil, asAppError(errors.New("admin authority is not configured"))
		}
		if err := uc.Admins.ConsumeProof(ctx, proof, adminID, domain.ProofPurposePayoutBatchApprove,
			domain.PayoutBatchApprovalRef(batchID, expectedState)); err != nil {
			return nil, err
		}
	}
	final := false
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		b, err := uc.Payouts.LockBatch(ctx, batchID)
		if err != nil {
			return err
		}
		from := b.Approval.State
		if final, err = domain.ApprovePayoutBatch(b, adminID, expectedState, uc.Now().UTC()); err != nil {
			return err
		}
		if err := uc.Payouts.SaveApproval(ctx, b, from); err != nil {
			return err
		}
		action := "payout_batch_approved"
		if !final {
			action = "payout_batch_first_approval"
		}
		return uc.Audit.Record(ctx, adminID, action, "payout_batch", b.ID,
			fmt.Sprintf("%s: %d %s, %d item(s)", from, b.TotalAmount, b.Currency, len(b.Items)))
	})
	if err != nil {
		return nil, payoutApprovalError(err)
	}
	uc.Log.Info().Str("payout_batch_id", batchID).Bool("approved", final).Msg("payout_batch_approval_recorded")
	return uc.GetBatch(ctx, adminID, batchID)
}

// ClosePayoutBatch rejects (another admin) or withdraws (its creator) a
// batch waiting for approval; its items are cancelled and their entries
// are unpaid again. No money has moved, so no proof is asked.
func (uc *SettlementUseCase) ClosePayoutBatch(ctx context.Context, adminID, batchID, reason string, withdraw bool) (*domain.PayoutBatch, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		b, err := uc.Payouts.LockBatch(ctx, batchID)
		if err != nil {
			return err
		}
		from := b.Approval.State
		if err := domain.ClosePayoutBatch(b, adminID, reason, withdraw, uc.Now().UTC()); err != nil {
			return err
		}
		if err := uc.Payouts.SaveApproval(ctx, b, from); err != nil {
			return err
		}
		why := *b.Approval.DecisionReason
		n, err := uc.Payouts.CancelPendingItems(ctx, b.ID, adminID, clipNote("Batch "+b.Approval.State+": "+why))
		if err != nil {
			return err
		}
		return uc.Audit.Record(ctx, adminID, "payout_batch_"+b.Approval.State, "payout_batch", b.ID,
			fmt.Sprintf("%d item(s) cancelled: %s", n, why))
	})
	if err != nil {
		return nil, payoutApprovalError(err)
	}
	uc.Log.Warn().Str("payout_batch_id", batchID).Bool("withdrawn", withdraw).Msg("payout_batch_closed")
	return uc.GetBatch(ctx, adminID, batchID)
}

// clipNote keeps an item note within its 500 characters.
func clipNote(s string) string {
	if r := []rune(s); len(r) > 500 {
		return string(r[:500])
	}
	return s
}
