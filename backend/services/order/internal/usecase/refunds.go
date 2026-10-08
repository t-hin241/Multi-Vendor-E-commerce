package usecase

import (
	"context"
	"regexp"
	"strings"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// RefundInput is an admin's refund request outside the return flow: a
// dispute on a paid vendor order, or returning a capture Order rejected
// (late payment on a cancelled order, or a second payment).
type RefundInput struct {
	OrderID       string
	VendorOrderID string
	PaymentID     string
	ReasonCode    string
	Amount        int64
	Reason        string
	// IdempotencyKey (optional) makes a resend of the same request after a
	// timeout return the refund already created.
	IdempotencyKey string
}

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,100}$`)

// sameRefundRequest tells whether a resend with an idempotency key asks for
// exactly the refund that key already created.
func sameRefundRequest(f *domain.Refund, in RefundInput) bool {
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	return f.ReasonCode == in.ReasonCode && f.Amount == in.Amount &&
		str(f.VendorOrderID) == in.VendorOrderID && str(f.PaymentID) == in.PaymentID
}

// AdminRequestRefund records a refund and queues it for Payment. Nothing
// is marked refunded until Payment confirms the money was returned.
func (uc *OrderUseCase) AdminRequestRefund(ctx context.Context, adminID string, in RefundInput) (*domain.Refund, error) {
	if in.IdempotencyKey != "" && !idempotencyKeyPattern.MatchString(in.IdempotencyKey) {
		return nil, apperror.Validation("Idempotency-Key must be 8-100 letters, digits or ._:-")
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	var refund *domain.Refund
	created := false
	err := uc.withOrder(ctx, in.OrderID, func(ctx context.Context) error {
		order, err := uc.findOrder(ctx, in.OrderID)
		if err != nil {
			return err
		}
		refund, created, err = uc.requestRefundLocked(ctx, adminID, order, in)
		return err
	})
	if err != nil {
		return nil, err
	}
	if !created {
		return refund, nil
	}
	uc.Log.Info().Str("order_id", in.OrderID).Str("refund_id", refund.ID).Str("admin_id", adminID).Int64("amount", refund.Amount).Msg("order_refund_requested")
	uc.runEffectsSoon(ctx, in.OrderID)
	return refund, nil
}

// requestRefundLocked records a refund under the order lock the caller
// holds: replays by idempotency key, checks what is still refundable,
// queues the request for Payment and audits it. created is false for a
// replay.
func (uc *OrderUseCase) requestRefundLocked(ctx context.Context, adminID string, order *domain.Order, in RefundInput) (refund *domain.Refund, created bool, err error) {
	err = func() error {
		if in.IdempotencyKey != "" {
			existing, err := uc.Refunds.FindByIdempotencyKey(ctx, order.ID, in.IdempotencyKey)
			if err != nil {
				return err
			}
			if existing != nil {
				if !sameRefundRequest(existing, in) {
					return apperror.Conflict("This Idempotency-Key was already used for a different refund request")
				}
				refund = existing
				return nil
			}
		}
		refund = &domain.Refund{OrderID: order.ID, ReasonCode: in.ReasonCode, Amount: in.Amount, Currency: order.Currency, RequestedBy: adminID}
		switch in.ReasonCode {
		case domain.RefundReasonDispute:
			if in.VendorOrderID == "" {
				return apperror.Validation("vendor_order_id is required for a dispute refund")
			}
			if !order.Status.PaidOrFurther() {
				return apperror.Conflict("Only a paid order can be refunded")
			}
			vo, err := uc.VendorOrders.FindByID(ctx, in.VendorOrderID)
			if err != nil || vo.OrderID != order.ID {
				return apperror.NotFound("Vendor order not found in this order")
			}
			refundable, err := uc.refundableFor(ctx, order, vo)
			if err != nil {
				return err
			}
			if refund.Reason, err = domain.ValidateRefundRequest(in.Amount, refundable, in.Reason); err != nil {
				return err
			}
			refund.VendorOrderID = &vo.ID
		case domain.RefundReasonLatePayment, domain.RefundReasonDuplicatePayment:
			if in.PaymentID == "" {
				return apperror.Validation("payment_id is required to refund a rejected payment")
			}
			p, err := uc.Payments.Find(ctx, in.PaymentID)
			if err != nil {
				return err
			}
			if p == nil || p.OrderID != order.ID || p.Outcome != domain.PaymentRejected {
				return apperror.Conflict("Only a payment Order rejected can be refunded this way")
			}
			if refund.Reason, err = domain.ValidateRefundRequest(in.Amount, p.Amount, in.Reason); err != nil {
				return err
			}
			refund.PaymentID, refund.Currency = &p.PaymentID, p.Currency
		default:
			return apperror.Validation("reason_code must be dispute, late_payment or duplicate_payment; returns are refunded from the return flow")
		}
		if in.IdempotencyKey != "" {
			refund.IdempotencyKey = &in.IdempotencyKey
		}
		if err := uc.createRefund(ctx, refund); err != nil {
			return err
		}
		created = true
		changes := map[string]any{"amount": refund.Amount, "currency": refund.Currency, "reason_code": refund.ReasonCode}
		if refund.VendorOrderID != nil {
			changes["vendor_order_id"] = *refund.VendorOrderID
		}
		if refund.PaymentID != nil {
			changes["payment_id"] = *refund.PaymentID
		}
		return uc.audit(ctx, domain.AdminAction{ActorID: adminID, Action: "refund_requested", EntityType: domain.AuditRefund,
			EntityID: refund.ID, OrderID: &order.ID, Reason: &refund.Reason, Changes: changes})
	}()
	return refund, created, err
}

// refundableFor is what can still be refunded for a vendor order: its paid
// total minus refunds already open or paid, capped by what remains of the
// order's applied capture.
func (uc *OrderUseCase) refundableFor(ctx context.Context, order *domain.Order, vo *domain.VendorOrder) (int64, error) {
	orderOpen, vendorOpen, err := uc.Refunds.OpenTotals(ctx, order.ID, vo.ID)
	if err != nil {
		return 0, err
	}
	captured, err := uc.appliedCapture(ctx, order)
	if err != nil {
		return 0, err
	}
	return max(min(vo.Total()-vendorOpen, captured-orderOpen), 0), nil
}

// appliedCapture is the money actually captured for the order. Orders paid
// before the capture ledger existed are assumed captured at their total.
func (uc *OrderUseCase) appliedCapture(ctx context.Context, order *domain.Order) (int64, error) {
	payments, err := uc.Payments.ListByOrder(ctx, order.ID)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, p := range payments {
		if p.Outcome == domain.PaymentApplied {
			total += p.Amount
		}
	}
	if total == 0 && order.Status.PaidOrFurther() {
		total = order.TotalAmount
	}
	return total, nil
}

// createRefund stores the refund and queues its submission to Payment in
// the caller's transaction.
func (uc *OrderUseCase) createRefund(ctx context.Context, refund *domain.Refund) error {
	if err := uc.Refunds.Create(ctx, refund); err != nil {
		return err
	}
	return uc.Effects.Enqueue(ctx, domain.Effect{OrderID: refund.OrderID, Kind: domain.EffectRequestRefund, Target: refund.ID})
}

// submitRefund is the request_refund effect: hand a requested refund to
// Payment. A refusal from Payment is final for this refund.
func (uc *OrderUseCase) submitRefund(ctx context.Context, refundID string) error {
	refund, err := uc.Refunds.FindByID(ctx, refundID)
	if err != nil {
		return appError(err)
	}
	if refund.Status != domain.RefundRequested {
		return nil
	}
	receipt, err := uc.Payment.RequestRefund(ctx, adapter.RefundRequest{
		RefundID: refund.ID, OrderID: refund.OrderID, PaymentID: refund.PaymentID, VendorOrderID: refund.VendorOrderID, Amount: refund.Amount,
		Currency: refund.Currency, Reason: refund.Reason, RequestedBy: refund.RequestedBy,
	})
	if err != nil {
		if !isPermanent(err) {
			return err
		}
		message := appError(err).Message
		rejectErr := uc.applyRefundOutcome(ctx, refund.OrderID, domain.RefundOutcome{RefundID: refund.ID, Status: domain.RefundRejected, FailureReason: message})
		return rejectErr
	}
	return uc.withOrder(ctx, refund.OrderID, func(ctx context.Context) error {
		current, err := uc.Refunds.FindByID(ctx, refund.ID)
		if err != nil {
			return err
		}
		if current.Status != domain.RefundRequested {
			return nil // Payment's outcome already arrived
		}
		return uc.Refunds.Transition(ctx, refund.ID, domain.RefundRequested, domain.RefundSubmitted, &receipt.PaymentRefundID, nil)
	})
}

// ApplyRefundOutcome records Payment's confirmed refund result. Replays of
// the same outcome are accepted; a contradicting outcome is refused.
func (uc *OrderUseCase) ApplyRefundOutcome(ctx context.Context, outcome domain.RefundOutcome) error {
	refund, err := uc.Refunds.FindByID(ctx, outcome.RefundID)
	if err != nil {
		return notFoundOrInternal(err, repository.ErrRefundNotFound, "Refund not found")
	}
	if outcome.Status != domain.RefundSucceeded && outcome.Status != domain.RefundFailed {
		return apperror.Validation("Refund outcome must be succeeded or failed")
	}
	if err := uc.applyRefundOutcome(ctx, refund.OrderID, outcome); err != nil {
		return err
	}
	uc.runEffectsSoon(ctx, refund.OrderID)
	return nil
}

func (uc *OrderUseCase) applyRefundOutcome(ctx context.Context, orderID string, outcome domain.RefundOutcome) error {
	return uc.withOrder(ctx, orderID, func(ctx context.Context) error {
		refund, err := uc.Refunds.FindByID(ctx, outcome.RefundID)
		if err != nil {
			return err
		}
		if refund.Status == outcome.Status {
			return nil
		}
		if !domain.CanApplyOutcome(refund.Status, outcome.Status) {
			return apperror.Conflict("Refund is already " + string(refund.Status))
		}
		if outcome.Status == domain.RefundSucceeded && (outcome.Amount != refund.Amount || outcome.Currency != refund.Currency) {
			uc.Log.Error().Str("refund_id", refund.ID).Msg("order_refund_outcome_mismatch")
			return apperror.Conflict("Refund outcome amount or currency does not match the request")
		}
		var paymentRefundID, failure *string
		if outcome.PaymentRefundID != "" {
			paymentRefundID = &outcome.PaymentRefundID
		}
		if msg := strings.TrimSpace(outcome.FailureReason); msg != "" {
			failure = &msg
		}
		if err := uc.Refunds.Transition(ctx, refund.ID, refund.Status, outcome.Status, paymentRefundID, failure); err != nil {
			return err
		}

		switch outcome.Status {
		case domain.RefundSucceeded:
			if err := uc.recordRefunded(ctx, refund); err != nil {
				return err
			}
			return uc.syncSupportResolution(ctx, domain.ResolutionRefund, refund.ID, true, nil)
		case domain.RefundFailed, domain.RefundRejected:
			if refund.ReturnRequestID != nil {
				if err := uc.moveReturn(ctx, *refund.ReturnRequestID, domain.ReturnRefundFailed, "system", nil, "refund_failed", failure); err != nil {
					return err
				}
			}
			return uc.syncSupportResolution(ctx, domain.ResolutionRefund, refund.ID, false, ptr("Refund "+string(outcome.Status)))
		}
		return nil
	})
}

// recordRefunded applies a confirmed refund: refunded amounts go up, a
// fully refunded vendor order becomes refunded, the parent is recomputed,
// a linked return is closed and the buyer is told.
func (uc *OrderUseCase) recordRefunded(ctx context.Context, refund *domain.Refund) error {
	if err := uc.Orders.AddRefunded(ctx, refund.OrderID, refund.Amount); err != nil {
		return err
	}
	if refund.VendorOrderID != nil {
		refunded, err := uc.VendorOrders.AddRefunded(ctx, *refund.VendorOrderID, refund.Amount)
		if err != nil {
			return err
		}
		vo, err := uc.VendorOrders.FindByID(ctx, *refund.VendorOrderID)
		if err != nil {
			return err
		}
		if refunded >= vo.Total() && domain.CanTransition(vo.Status, domain.StatusRefunded) {
			if err := uc.VendorOrders.TransitionStatus(ctx, vo.ID, vo.Status, domain.StatusRefunded); err != nil {
				return err
			}
			if err := uc.Effects.Enqueue(ctx, domain.Effect{OrderID: refund.OrderID, Kind: domain.EffectCancelShipment, Target: vo.ID}); err != nil {
				return err
			}
		}
		if err := uc.recomputeOrderStatus(ctx, refund.OrderID); err != nil {
			return err
		}
	}
	if refund.ReturnRequestID != nil {
		if err := uc.moveReturn(ctx, *refund.ReturnRequestID, domain.ReturnRefunded, "system", nil, "refund_succeeded", nil); err != nil {
			return err
		}
	}
	order, err := uc.findOrder(ctx, refund.OrderID)
	if err != nil {
		return err
	}
	notice := domain.NewNotifyEffect(order.ID, order.BuyerID, notifyOrderRefunded)
	notice.Target = notifyOrderRefunded + ":" + refund.ID
	return uc.Effects.Enqueue(ctx, notice)
}

// ListRefunds lists refunds for admin, optionally by status.
func (uc *OrderUseCase) ListRefunds(ctx context.Context, status string, limit, offset int) ([]*domain.Refund, error) {
	if status != "" && !validRefundStatus(status) {
		return nil, apperror.Validation("Invalid refund status filter")
	}
	refunds, err := uc.Refunds.List(ctx, status, limit, offset)
	return refunds, asError(err)
}

func validRefundStatus(s string) bool {
	switch domain.RefundStatus(s) {
	case domain.RefundRequested, domain.RefundSubmitted, domain.RefundSucceeded, domain.RefundFailed, domain.RefundRejected:
		return true
	}
	return false
}

// ListPaymentExceptions lists captures Order rejected that still have no
// open refund: money to return or review.
func (uc *OrderUseCase) ListPaymentExceptions(ctx context.Context, limit, offset int) ([]*domain.OrderPayment, error) {
	payments, err := uc.Payments.ListRejected(ctx, limit, offset)
	return payments, asError(err)
}
