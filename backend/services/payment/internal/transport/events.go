package transport

import (
	"context"

	"github.com/jackc/pgx/v5"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
	"shopee/backend/services/payment/internal/usecase"
)

// SettleableHandler records a completed vendor order in the settlement
// ledger from order.vendor_order_settleable (once per vendor order).
func SettleableHandler(settlement *usecase.SettlementUseCase) eventbus.Handler {
	return func(ctx context.Context, tx pgx.Tx, env eventbus.Envelope) error {
		var s events.Settlement
		if err := env.Decode(&s); err != nil {
			return err
		}
		_, err := settlement.IngestVendorOrder(repository.WithTx(ctx, tx), domain.SettlementOrder{
			VendorOrderID: s.VendorOrderID, OrderID: s.OrderID, VendorID: s.VendorID, Currency: s.Currency,
			SubtotalAmount: s.SubtotalAmount, ShippingAmount: s.ShippingAmount, CommissionAmount: s.CommissionAmount,
			CommissionRateBps: s.CommissionRateBps, CommissionRuleVersion: s.CommissionRuleVersion,
			CompletedAt: s.CompletedAt.UTC(), EligibleAt: s.EligibleAt.UTC(),
		})
		return err
	}
}

// OutcomeRejectedHandler puts a payment or refund outcome Order refused up
// for review (order.payment_outcome_rejected), as a refusal over HTTP did.
func OutcomeRejectedHandler(orders repository.OrderSync, refunds repository.RefundSync) eventbus.Handler {
	return func(ctx context.Context, tx pgx.Tx, env eventbus.Envelope) error {
		var r events.OutcomeRejection
		if err := env.Decode(&r); err != nil {
			return err
		}
		ctx = repository.WithTx(ctx, tx)
		switch {
		case r.Kind == "payment" && r.PaymentID != "":
			return orders.MarkRejected(ctx, r.PaymentID)
		case r.Kind == "refund" && r.PaymentRefundID != "":
			return refunds.MarkRejected(ctx, r.PaymentRefundID)
		}
		return apperror.Validation("Unknown rejected outcome")
	}
}
