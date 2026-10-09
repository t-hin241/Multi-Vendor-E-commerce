package transport

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
	"shopee/backend/services/order/internal/usecase"
)

// Order's event consumers (PLT-03). Each applies the event through the
// same use case as the internal HTTP route, in the inbox transaction.

// PolicyPublishedHandler: Vendor published a policy version (AF-02); it
// joins Order's read model, used by the next checkouts.
func PolicyPublishedHandler(orders *usecase.OrderUseCase) eventbus.Handler {
	return func(ctx context.Context, tx pgx.Tx, env eventbus.Envelope) error {
		var p events.PolicyPublication
		if err := env.Decode(&p); err != nil {
			return err
		}
		return orders.ApplyPolicyPublished(repository.WithTx(ctx, tx), policyVersionOf(p))
	}
}

func policyVersionOf(p events.PolicyPublication) domain.PolicyVersion {
	return domain.PolicyVersion{PolicyID: p.PolicyID, Scope: p.Scope, VendorID: p.VendorID, Kind: p.Kind, Version: p.Version,
		ContentHash: p.ContentHash, RuleRefs: p.RuleRefs, EffectiveAt: p.EffectiveAt}
}

// ReservationExpiredHandler: Inventory's hold for an order expired.
func ReservationExpiredHandler(orders *usecase.OrderUseCase) eventbus.Handler {
	return func(ctx context.Context, tx pgx.Tx, env eventbus.Envelope) error {
		var r events.ReservationExpiry
		if err := env.Decode(&r); err != nil {
			return err
		}
		if r.Type != "ReservationExpired" || r.OrderID == "" {
			return apperror.Validation("Invalid inventory event")
		}
		return orders.ReservationExpired(repository.WithTx(ctx, tx), r.OrderID)
	}
}

// ShipmentChangedHandler: a package shipped, was delivered or came back.
func ShipmentChangedHandler(orders *usecase.OrderUseCase) eventbus.Handler {
	return func(ctx context.Context, tx pgx.Tx, env eventbus.Envelope) error {
		var f events.ShipmentFact
		if err := env.Decode(&f); err != nil {
			return err
		}
		switch f.Type {
		case "shipped", "delivered", "returned":
		default:
			return apperror.Validation("Invalid shipment event type")
		}
		return orders.ApplyShipmentEvent(repository.WithTx(ctx, tx), usecase.ShipmentEvent{EventID: f.EventID, ShipmentID: f.ShipmentID,
			VendorOrderID: f.VendorOrderID, Type: f.Type, OccurredAt: f.OccurredAt})
	}
}

// ShipmentExceptionHandler: delivery failed for good (AF-04); the case
// is opened or updated in the inbox transaction.
func ShipmentExceptionHandler(orders *usecase.OrderUseCase) eventbus.Handler {
	return func(ctx context.Context, tx pgx.Tx, env eventbus.Envelope) error {
		var x events.ShipmentException
		if err := env.Decode(&x); err != nil {
			return err
		}
		if x.ShipmentID == "" || x.VendorOrderID == "" || !domain.ValidExceptionFact(x.ExceptionType) {
			return apperror.Validation("Invalid shipment exception")
		}
		return orders.ApplyShipmentException(repository.WithTx(ctx, tx), usecase.ShipmentExceptionFact{EventID: x.EventID, ShipmentID: x.ShipmentID,
			VendorOrderID: x.VendorOrderID, Type: x.ExceptionType, AttemptNo: x.AttemptNo, FailedAttempts: x.FailedAttempts, Reason: x.Reason,
			OccurredAt: x.OccurredAt})
	}
}

// PaymentOutcomeHandler: a payment was captured or failed. When Order
// refuses it (amount mismatch, order no longer payable), nothing of the
// attempt is kept and Payment is told through a durable effect, so the
// payment goes up for review there (as the HTTP refusal did).
func PaymentOutcomeHandler(orders *usecase.OrderUseCase) eventbus.Handler {
	return func(ctx context.Context, tx pgx.Tx, env eventbus.Envelope) error {
		var p events.PaymentResult
		if err := env.Decode(&p); err != nil {
			return err
		}
		if p.OrderID == "" || p.PaymentID == "" {
			return apperror.Validation("Payment outcome names no order or payment")
		}
		err := attempt(ctx, tx, func(ctx context.Context) error {
			switch p.Outcome {
			case "captured":
				if p.Amount < 1 || len(p.Currency) != 3 {
					return apperror.Validation("A capture needs an amount and currency")
				}
				_, err := orders.MarkPaid(ctx, p.OrderID, &domain.PaymentCapture{PaymentID: p.PaymentID, Amount: p.Amount, Currency: strings.ToUpper(p.Currency)})
				return err
			case "failed":
				reason := p.Reason
				if reason == "" {
					reason = "Payment failed"
				}
				_, err := orders.MarkPaymentFailed(ctx, p.OrderID, reason)
				return err
			}
			return apperror.Validation("Unknown payment outcome")
		})
		if err == nil || !eventbus.IsPermanent(err) || notFound(err) {
			return err // an unknown order parks the event with Order's refusal
		}
		return reportRejection(repository.WithTx(ctx, tx), orders, p.OrderID, "payment-"+p.PaymentID,
			domain.RejectedOutcomePayload{Kind: "payment", PaymentID: p.PaymentID, Reason: refusal(err)})
	}
}

// RefundOutcomeHandler: Payment confirmed a refund or it failed. A refused
// outcome is reported back like a refused payment.
func RefundOutcomeHandler(orders *usecase.OrderUseCase) eventbus.Handler {
	return func(ctx context.Context, tx pgx.Tx, env eventbus.Envelope) error {
		var r events.RefundResult
		if err := env.Decode(&r); err != nil {
			return err
		}
		if r.OrderRefundID == "" || r.PaymentRefundID == "" || r.Amount < 1 || len(r.Currency) != 3 {
			return apperror.Validation("Invalid refund outcome")
		}
		err := attempt(ctx, tx, func(ctx context.Context) error {
			return orders.ApplyRefundOutcome(ctx, domain.RefundOutcome{RefundID: r.OrderRefundID, PaymentRefundID: r.PaymentRefundID,
				Status: domain.RefundStatus(r.Status), Amount: r.Amount, Currency: strings.ToUpper(r.Currency), FailureReason: r.FailureReason})
		})
		if err == nil || !eventbus.IsPermanent(err) {
			return err
		}
		refund, findErr := orders.Refunds.FindByID(repository.WithTx(ctx, tx), r.OrderRefundID)
		if findErr != nil {
			return err // unknown refund: the event parks with Order's refusal
		}
		return reportRejection(repository.WithTx(ctx, tx), orders, refund.OrderID, "refund-"+r.PaymentRefundID,
			domain.RejectedOutcomePayload{Kind: "refund", PaymentRefundID: r.PaymentRefundID, Reason: refusal(err)})
	}
}

func notFound(err error) bool {
	var app *apperror.Error
	return errors.As(err, &app) && app.Code == apperror.CodeNotFound
}

// attempt runs fn in a savepoint of tx: a refusal leaves no partial write.
func attempt(ctx context.Context, tx pgx.Tx, fn func(context.Context) error) error {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(repository.WithTx(ctx, sp)); err != nil {
		_ = sp.Rollback(ctx)
		return err
	}
	return sp.Commit(ctx)
}

func reportRejection(ctx context.Context, orders *usecase.OrderUseCase, orderID, target string, p domain.RejectedOutcomePayload) error {
	payload, err := json.Marshal(p)
	if err != nil {
		return err
	}
	orders.Log.Warn().Str("order_id", orderID).Str("target", target).Str("reason", p.Reason).Msg("order_outcome_rejected_reported")
	return orders.Effects.Enqueue(ctx, domain.Effect{OrderID: orderID, Kind: domain.EffectReportRejectedOutcome, Target: target, Payload: payload})
}

func refusal(err error) string {
	msg := err.Error()
	var app *apperror.Error
	if errors.As(err, &app) {
		msg = app.Message
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}
