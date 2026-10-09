package usecase

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/events"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// CancellationPort stores AF-03 requests and the vendor order's handover
// claim.
type CancellationPort interface {
	Create(ctx context.Context, c *domain.CancellationRequest) error
	FindByID(ctx context.Context, id string) (*domain.CancellationRequest, error)
	FindByKey(ctx context.Context, requesterID, key string) (*domain.CancellationRequest, error)
	FindOpen(ctx context.Context, vendorOrderID string) (*domain.CancellationRequest, error)
	FindByRefund(ctx context.Context, refundID string) (*domain.CancellationRequest, error)
	ListByOrder(ctx context.Context, orderID string) ([]*domain.CancellationRequest, error)
	ListForVendor(ctx context.Context, vendorID, status string, limit, offset int) ([]*domain.CancellationRequest, error)
	List(ctx context.Context, status string, limit, offset int) ([]*domain.CancellationRequest, error)
	Save(ctx context.Context, c *domain.CancellationRequest) error
	SetHold(ctx context.Context, id string, holdID *string, from []string, to string, note *string) (bool, error)
	AddEvent(ctx context.Context, e *domain.CancellationEvent) error
	ListEvents(ctx context.Context, requestID string) ([]*domain.CancellationEvent, error)
	Handover(ctx context.Context, vendorOrderID string) (*time.Time, *string, error)
	ClaimHandover(ctx context.Context, vendorOrderID, shipmentID string) error
	Counts(ctx context.Context, olderThan time.Time) (open, needsReview, stale int64, err error)
}

// FulfillmentStopper is Shipment stopping a vendor order before handover.
type FulfillmentStopper interface {
	StopFulfillment(ctx context.Context, vendorOrderID, operationID string) (string, error)
}

// StockRecoverer is Inventory putting back units that never left.
type StockRecoverer interface {
	RestockRecovery(ctx context.Context, recoveryID, productID string, variantID *string, quantity int64) error
}

const (
	notifyCancellationRequested = "cancellation_requested"
	notifyCancellationApproved  = "cancellation_approved"
	notifyCancellationRejected  = "cancellation_rejected"

	// cancellationTarget prefixes a hold effect's target for a request.
	cancellationTarget = "cancellation:"
)

func cancellationError(err error) error {
	switch {
	case errors.Is(err, repository.ErrCancellationNotFound):
		return apperror.NotFound("Cancellation request not found")
	case errors.Is(err, repository.ErrStaleState):
		return domain.CancellationChanged()
	case errors.Is(err, repository.ErrSupportKeyTaken):
		return domain.SupportKeyReused()
	}
	return asError(err)
}

// CancellationInput is what the buyer or vendor sends.
type CancellationInput struct {
	ReasonCode     string
	Reason         string
	IdempotencyKey string
}

// RequestCancellation opens a request on a paid vendor order not handed
// over yet. Under the order lock it is ordered against Shipment's handover
// claim: once written, no handover is granted. The payout hold is acquired
// before the request waits for a decision (00 §6.1).
func (uc *OrderUseCase) RequestCancellation(ctx context.Context, actor SupportActor, vendorOrderID string, in CancellationInput) (*domain.CancellationRequest, bool, error) {
	if uc.Cancellations == nil || !uc.PaidCancellation {
		return nil, false, domain.CancellationDisabled()
	}
	reasons := domain.BuyerCancellationReasons
	if actor.Role == "vendor" {
		reasons = domain.VendorCancellationReasons
	}
	if !reasons[in.ReasonCode] {
		return nil, false, apperror.Validation("Unknown reason_code for this requester")
	}
	reason, err := domain.ValidateNote(in.Reason, 1000, true, "A reason")
	if err != nil {
		return nil, false, err
	}
	if err := validSupportKey(in.IdempotencyKey); err != nil {
		return nil, false, err
	}
	vo, err := uc.VendorOrders.FindByID(ctx, vendorOrderID)
	if err != nil {
		return nil, false, notFoundOrInternal(err, repository.ErrVendorOrderNotFound, "Order not found")
	}
	if actor.Role == "vendor" {
		if _, err := uc.Vendors.GetApprovedVendorID(ctx, actor.ID, vo.VendorID, shopaccess.OrdersFulfill); err != nil {
			if shopaccess.IsUnavailable(err) {
				return nil, false, err
			}
			return nil, false, apperror.NotFound("Order not found")
		}
	}
	hash := requestHash(vendorOrderID, in.ReasonCode, *reason)
	var result *domain.CancellationRequest
	replayed := false
	err = uc.withOrder(ctx, vo.OrderID, func(ctx context.Context) error {
		if in.IdempotencyKey != "" {
			existing, err := uc.Cancellations.FindByKey(ctx, actor.ID, in.IdempotencyKey)
			if err != nil {
				return err
			}
			if existing != nil {
				if existing.RequestHash == nil || *existing.RequestHash != hash {
					return domain.SupportKeyReused()
				}
				result, replayed = existing, true
				return nil
			}
		}
		order, err := uc.findOrder(ctx, vo.OrderID)
		if err != nil {
			return err
		}
		if actor.Role == "buyer" && order.BuyerID != actor.ID {
			return apperror.NotFound("Order not found")
		}
		current, err := uc.VendorOrders.FindByID(ctx, vendorOrderID)
		if err != nil {
			return err
		}
		open, err := uc.Cancellations.FindOpen(ctx, current.ID)
		if err != nil {
			return err
		}
		if open != nil {
			return domain.CancellationExists()
		}
		claimed, _, err := uc.Cancellations.Handover(ctx, current.ID)
		if err != nil {
			return err
		}
		if err := domain.CheckCancellable(current, claimed != nil); err != nil {
			return err
		}
		c := &domain.CancellationRequest{OrderID: order.ID, VendorOrderID: current.ID, VendorID: current.VendorID, BuyerID: order.BuyerID,
			Origin: actor.Role, RequestedBy: actor.ID, ReasonCode: in.ReasonCode, Reason: *reason, Status: domain.CancelRequested,
			PolicyVersion: domain.CancellationPolicyVersion, RequestHash: &hash}
		if in.IdempotencyKey != "" {
			c.IdempotencyKey = &in.IdempotencyKey
		}
		ledger := uc.holdLedger()
		if ledger {
			holdID, preparing := uuid.NewString(), domain.HoldPreparing
			c.Status, c.HoldID, c.HoldStatus = domain.CancelPreparing, &holdID, &preparing
		}
		if err := uc.Cancellations.Create(ctx, c); err != nil {
			return err
		}
		if err := uc.Cancellations.AddEvent(ctx, &domain.CancellationEvent{RequestID: c.ID, ActorID: &actor.ID, ActorRole: actor.Role,
			Action: "requested", ToStatus: string(c.Status)}); err != nil {
			return err
		}
		if ledger {
			if err := uc.Effects.Enqueue(ctx, domain.Effect{OrderID: order.ID, Kind: domain.EffectAcquireSettlementHold,
				Target: cancellationTarget + c.ID}); err != nil {
				return err
			}
		}
		notice := domain.NewNotifyEffect(order.ID, order.BuyerID, notifyCancellationRequested)
		notice.Target = notifyCancellationRequested + ":" + c.ID
		if err := uc.Effects.Enqueue(ctx, notice); err != nil {
			return err
		}
		if c.Origin != "vendor" {
			if err := uc.noticeVendor(ctx, order.ID, c.VendorID, c.VendorOrderID, events.VendorActionCancellationRequested, c.ID); err != nil {
				return err
			}
		}
		result = c
		return nil
	})
	if err != nil {
		return nil, false, cancellationError(err)
	}
	if !replayed {
		uc.Log.Info().Str("request_id", result.ID).Str("vendor_order_id", vendorOrderID).Str("origin", result.Origin).
			Str("reason_code", result.ReasonCode).Msg("order_cancellation_requested")
		uc.runEffectsSoon(ctx, result.OrderID)
	}
	return result, replayed, nil
}

// CancellationDetail is a request with the timeline its caller may see.
type CancellationDetail struct {
	Request *domain.CancellationRequest
	Events  []*domain.CancellationEvent
}

// GetCancellation: the buyer of the order, the selling shop (orders.read)
// or an admin. Notes and other actors' ids are admin-only.
func (uc *OrderUseCase) GetCancellation(ctx context.Context, actor SupportActor, id string) (*CancellationDetail, error) {
	if uc.Cancellations == nil {
		return nil, apperror.NotFound("Cancellation request not found")
	}
	c, err := uc.Cancellations.FindByID(ctx, id)
	if err != nil {
		return nil, cancellationError(err)
	}
	switch actor.Role {
	case "buyer":
		if c.BuyerID != actor.ID {
			return nil, apperror.NotFound("Cancellation request not found")
		}
	case "vendor":
		if _, err := uc.Vendors.GetApprovedVendorID(ctx, actor.ID, c.VendorID, shopaccess.OrdersRead); err != nil {
			if shopaccess.IsUnavailable(err) {
				return nil, err
			}
			return nil, apperror.NotFound("Cancellation request not found")
		}
	}
	events, err := uc.Cancellations.ListEvents(ctx, c.ID)
	if err != nil {
		return nil, appError(err)
	}
	if actor.Role != "admin" {
		visible := make([]*domain.CancellationEvent, 0, len(events))
		for _, e := range events {
			if strings.HasPrefix(e.Action, "settlement_hold_") {
				continue
			}
			copied := *e
			copied.Note = nil
			if copied.ActorID != nil && *copied.ActorID != actor.ID {
				copied.ActorID = nil
			}
			visible = append(visible, &copied)
		}
		events = visible
		shown := *c
		shown.HoldID, shown.HoldStatus, shown.HoldNote, shown.ReviewReason, shown.DecidedBy = nil, nil, nil, nil, nil
		c = &shown
	}
	return &CancellationDetail{Request: c, Events: events}, nil
}

// ListOrderCancellations lists the buyer's requests on one order.
func (uc *OrderUseCase) ListOrderCancellations(ctx context.Context, buyerID, orderID string) ([]*domain.CancellationRequest, error) {
	if uc.Cancellations == nil {
		return []*domain.CancellationRequest{}, nil
	}
	order, err := uc.findOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order.BuyerID != buyerID {
		return nil, apperror.NotFound("Order not found")
	}
	out, err := uc.Cancellations.ListByOrder(ctx, orderID)
	if err != nil {
		return nil, appError(err)
	}
	for i, c := range out {
		shown := *c
		shown.HoldID, shown.HoldStatus, shown.HoldNote, shown.ReviewReason, shown.DecidedBy = nil, nil, nil, nil, nil
		out[i] = &shown
	}
	return out, nil
}

func (uc *OrderUseCase) ListVendorCancellations(ctx context.Context, userID, vendorID, status string, limit, offset int) ([]*domain.CancellationRequest, error) {
	if uc.Cancellations == nil {
		return []*domain.CancellationRequest{}, nil
	}
	vendorID, err := uc.Vendors.GetApprovedVendorID(ctx, userID, vendorID, shopaccess.OrdersRead)
	if err != nil {
		return nil, err
	}
	out, err := uc.Cancellations.ListForVendor(ctx, vendorID, status, limit, offset)
	return out, cancellationError(err)
}

func (uc *OrderUseCase) ListCancellations(ctx context.Context, status string, limit, offset int) ([]*domain.CancellationRequest, error) {
	if uc.Cancellations == nil {
		return []*domain.CancellationRequest{}, nil
	}
	out, err := uc.Cancellations.List(ctx, status, limit, offset)
	return out, cancellationError(err)
}

// CancellationDecision is an admin's decision.
type CancellationDecision struct {
	Decision        string // approve, reject, retry_refund
	Reason          string
	ExpectedVersion int64
	// Restock: the units never left the warehouse and go back to stock.
	// Default: yes for a buyer's request, no for a vendor's report.
	Restock *bool
}

// DecideCancellation approves (Shipment is then stopped, stock put back,
// refund requested), rejects (the package may ship again) or retries a
// failed refund. Money is never reported back before Payment confirms.
func (uc *OrderUseCase) DecideCancellation(ctx context.Context, adminID, id string, in CancellationDecision) (*domain.CancellationRequest, error) {
	if uc.Cancellations == nil {
		return nil, apperror.NotFound("Cancellation request not found")
	}
	reason, err := adminReason(in.Reason)
	if err != nil {
		return nil, err
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	first, err := uc.Cancellations.FindByID(ctx, id)
	if err != nil {
		return nil, cancellationError(err)
	}
	var result *domain.CancellationRequest
	err = uc.withOrder(ctx, first.OrderID, func(ctx context.Context) error {
		c, err := uc.Cancellations.FindByID(ctx, id)
		if err != nil {
			return err
		}
		if c.Version != in.ExpectedVersion {
			return domain.CancellationChanged()
		}
		now := uc.Now().UTC()
		switch in.Decision {
		case "approve":
			if c.Status == domain.CancelPreparing {
				return domain.HoldUnavailable()
			}
			if c.Status != domain.CancelRequested {
				return apperror.Conflict("Only a request waiting for a decision can be approved")
			}
			restock := c.Origin == "buyer"
			if in.Restock != nil {
				restock = *in.Restock
			}
			c.Restock, c.DecidedBy, c.DecidedAt, c.DecisionReason = &restock, &adminID, &now, reason
			if err := uc.moveCancellation(ctx, c, domain.CancelStopping, "admin", &adminID, "approved", reason); err != nil {
				return err
			}
			if err := uc.Effects.Enqueue(ctx, domain.Effect{OrderID: c.OrderID, Kind: domain.EffectStopFulfillment, Target: c.ID}); err != nil {
				return err
			}
		case "reject":
			if c.RefundID != nil {
				return apperror.Conflict("A refund was already requested for this cancellation; retry the refund instead")
			}
			if c.Status != domain.CancelPreparing && c.Status != domain.CancelRequested && c.Status != domain.CancelNeedsReview {
				return apperror.Conflict("This request can no longer be rejected")
			}
			c.DecidedBy, c.DecidedAt, c.DecisionReason = &adminID, &now, reason
			if err := uc.moveCancellation(ctx, c, domain.CancelRejected, "admin", &adminID, "rejected", reason); err != nil {
				return err
			}
			if err := uc.releaseCancellationHold(ctx, c); err != nil {
				return err
			}
			notice := domain.NewNotifyEffect(c.OrderID, c.BuyerID, notifyCancellationRejected)
			notice.Target = notifyCancellationRejected + ":" + c.ID
			if err := uc.Effects.Enqueue(ctx, notice); err != nil {
				return err
			}
		case "retry_refund":
			if c.Status != domain.CancelNeedsReview || c.RefundID == nil {
				return apperror.Conflict("Only a cancellation whose refund failed can retry it")
			}
			if err := uc.requestCancellationRefund(ctx, c, adminID, reason); err != nil {
				return err
			}
		default:
			return apperror.Validation("decision must be approve, reject or retry_refund")
		}
		result = c
		return uc.audit(ctx, domain.AdminAction{ActorID: adminID, Action: "cancellation_" + in.Decision, EntityType: domain.AuditCancellation,
			EntityID: c.ID, OrderID: &c.OrderID, Reason: reason, Changes: map[string]any{"status": string(c.Status), "restock": c.Restock}})
	})
	if err != nil {
		return nil, cancellationError(err)
	}
	uc.Log.Info().Str("request_id", id).Str("decision", in.Decision).Str("status", string(result.Status)).Msg("order_cancellation_decided")
	uc.runEffectsSoon(ctx, result.OrderID)
	return result, nil
}

// moveCancellation applies one transition with its timeline entry.
func (uc *OrderUseCase) moveCancellation(ctx context.Context, c *domain.CancellationRequest, to domain.CancellationStatus, role string,
	actor *string, action string, note *string) error {
	if !domain.CanTransitionCancellation(c.Status, to) {
		return apperror.Conflict("Cannot move this request from " + string(c.Status) + " to " + string(to))
	}
	from := c.Status
	c.Status = to
	if to == domain.CancelResolved {
		now := uc.Now().UTC()
		c.ResolvedAt = &now
	}
	if err := uc.Cancellations.Save(ctx, c); err != nil {
		c.Status = from
		return err
	}
	fromStatus := string(from)
	return uc.Cancellations.AddEvent(ctx, &domain.CancellationEvent{RequestID: c.ID, ActorID: actor, ActorRole: role, Action: action,
		FromStatus: &fromStatus, ToStatus: string(to), Note: note})
}

// stopFulfillment is the stop effect: ask Shipment, then under the lock
// either go on (stopped: restock, refund) or hand the request to an admin.
func (uc *OrderUseCase) stopFulfillment(ctx context.Context, e *domain.Effect) error {
	if uc.Cancellations == nil || uc.Stops == nil {
		return errHoldsNotWired
	}
	c, err := uc.Cancellations.FindByID(ctx, e.Target)
	if err != nil {
		return appError(err)
	}
	if c.Status != domain.CancelStopping {
		return nil
	}
	result, err := uc.Stops.StopFulfillment(ctx, c.VendorOrderID, "cancellation:"+c.ID)
	if err != nil {
		return err
	}
	return uc.withOrder(ctx, c.OrderID, func(ctx context.Context) error {
		c, err := uc.Cancellations.FindByID(ctx, e.Target)
		if err != nil || c.Status != domain.CancelStopping {
			return err
		}
		c.StopResult = &result
		if result != domain.StopStopped {
			note := "The package was already " + strings.ReplaceAll(result, "_", " ") + "; it cannot be cancelled before handover"
			c.ReviewReason = &note
			uc.Log.Warn().Str("request_id", c.ID).Str("result", result).Msg("order_cancellation_already_handed_over")
			return uc.moveCancellation(ctx, c, domain.CancelNeedsReview, "system", nil, "stop_refused", &note)
		}
		if err := uc.moveCancellation(ctx, c, domain.CancelApproved, "system", nil, "fulfillment_stopped", nil); err != nil {
			return err
		}
		if c.Restock != nil && *c.Restock {
			ref := cancellationTarget + c.ID
			c.RecoveryRef = &ref
			if err := uc.Effects.Enqueue(ctx, domain.Effect{OrderID: c.OrderID, Kind: domain.EffectRecoverCancelledStock, Target: c.ID}); err != nil {
				return err
			}
		}
		notice := domain.NewNotifyEffect(c.OrderID, c.BuyerID, notifyCancellationApproved)
		notice.Target = notifyCancellationApproved + ":" + c.ID
		if err := uc.Effects.Enqueue(ctx, notice); err != nil {
			return err
		}
		return uc.requestCancellationRefund(ctx, c, *c.DecidedBy, ptr("Paid order cancelled before handover"))
	})
}

// requestCancellationRefund refunds what is left of the vendor order
// (merchandise and the shipping fee paid, 02) through Payment.
func (uc *OrderUseCase) requestCancellationRefund(ctx context.Context, c *domain.CancellationRequest, actorID string, note *string) error {
	order, err := uc.findOrder(ctx, c.OrderID)
	if err != nil {
		return err
	}
	vo, err := uc.VendorOrders.FindByID(ctx, c.VendorOrderID)
	if err != nil {
		return err
	}
	amount, err := uc.refundableFor(ctx, order, vo)
	if err != nil {
		return err
	}
	if amount <= 0 {
		// Nothing left to refund (already refunded elsewhere).
		if err := uc.moveCancellation(ctx, c, domain.CancelResolved, "system", nil, "nothing_to_refund", note); err != nil {
			return err
		}
		return uc.releaseCancellationHold(ctx, c)
	}
	refund := &domain.Refund{OrderID: order.ID, VendorOrderID: &vo.ID, ReasonCode: domain.RefundReasonCancellation, Amount: amount,
		Currency: order.Currency, Reason: "Cancellation " + c.ID + " (" + c.ReasonCode + ")", RequestedBy: actorID}
	if err := uc.createRefund(ctx, refund); err != nil {
		return err
	}
	c.RefundID, c.ReviewReason = &refund.ID, nil
	return uc.moveCancellation(ctx, c, domain.CancelRefundPending, "system", nil, "refund_requested", note)
}

// recoverCancelledStock is the restock effect: every item of the vendor
// order goes back once (recovery id per item).
func (uc *OrderUseCase) recoverCancelledStock(ctx context.Context, e *domain.Effect) error {
	if uc.Cancellations == nil || uc.Recoveries == nil {
		return errHoldsNotWired
	}
	c, err := uc.Cancellations.FindByID(ctx, e.Target)
	if err != nil {
		return appError(err)
	}
	if c.Restock == nil || !*c.Restock || c.StopResult == nil || *c.StopResult != domain.StopStopped {
		return nil
	}
	items, err := uc.Orders.ListItemsByOrder(ctx, c.OrderID)
	if err != nil {
		return appError(err)
	}
	restocked := 0
	for _, item := range items {
		if item.VendorOrderID != c.VendorOrderID {
			continue
		}
		if err := uc.Recoveries.RestockRecovery(ctx, domain.RecoveryID(c.ID, item.ID), item.ProductID, item.VariantID, item.Quantity); err != nil {
			return err
		}
		restocked++
	}
	uc.Log.Info().Str("request_id", c.ID).Int("items", restocked).Msg("order_cancellation_stock_recovered")
	return nil
}

// syncCancellationRefund follows a cancellation's refund outcome, in the
// outcome's transaction under the order lock: succeeded resolves the
// request and releases its hold; failed hands it to an admin.
func (uc *OrderUseCase) syncCancellationRefund(ctx context.Context, refund *domain.Refund, succeeded bool, note *string) error {
	if uc.Cancellations == nil {
		return nil
	}
	c, err := uc.Cancellations.FindByRefund(ctx, refund.ID)
	if err != nil || c == nil {
		return err
	}
	c, err = uc.Cancellations.FindByID(ctx, c.ID)
	if err != nil || c.Status != domain.CancelRefundPending {
		return err
	}
	if succeeded {
		if err := uc.moveCancellation(ctx, c, domain.CancelResolved, "system", nil, "refund_succeeded", nil); err != nil {
			return err
		}
		return uc.releaseCancellationHold(ctx, c)
	}
	c.ReviewReason = note
	uc.Log.Warn().Str("request_id", c.ID).Str("refund_id", refund.ID).Msg("order_cancellation_refund_failed")
	return uc.moveCancellation(ctx, c, domain.CancelNeedsReview, "system", nil, "refund_failed", note)
}

// cancellationOpenFor reports an open request on the vendor order (the
// handover fence).
func (uc *OrderUseCase) cancellationOpenFor(ctx context.Context, vendorOrderID string) (bool, error) {
	if uc.Cancellations == nil {
		return false, nil
	}
	open, err := uc.Cancellations.FindOpen(ctx, vendorOrderID)
	return open != nil, err
}

// ClaimHandover is Shipment claiming the fulfillment grant before handing
// the package over. Under the order lock: refused while a cancellation is
// open or the vendor order cannot ship; otherwise recorded, after which no
// cancellation before handover is accepted.
func (uc *OrderUseCase) ClaimHandover(ctx context.Context, vendorOrderID, shipmentID string) error {
	if _, err := uuid.Parse(shipmentID); err != nil {
		return apperror.Validation("shipment_id must be a UUID")
	}
	vo, err := uc.VendorOrders.FindByID(ctx, vendorOrderID)
	if err != nil {
		return notFoundOrInternal(err, repository.ErrVendorOrderNotFound, "Order not found")
	}
	return uc.withOrder(ctx, vo.OrderID, func(ctx context.Context) error {
		current, err := uc.VendorOrders.FindByID(ctx, vendorOrderID)
		if err != nil {
			return err
		}
		// AF-04: with a delivery exception open, only its redelivery ships.
		if handled, err := uc.deliveryHandoverAllowed(ctx, current.ID, shipmentID); handled || err != nil {
			return err
		}
		order, err := uc.findOrder(ctx, current.OrderID)
		if err != nil {
			return err
		}
		if !current.Fulfillable() || order.CheckoutState != domain.CheckoutReady {
			return apperror.Conflict("Order does not allow this package to ship (not paid, stock not committed, or cancelled)")
		}
		if uc.Cancellations == nil {
			return nil
		}
		open, err := uc.Cancellations.FindOpen(ctx, current.ID)
		if err != nil {
			return err
		}
		if open != nil {
			return domain.CancellationPending()
		}
		return uc.Cancellations.ClaimHandover(ctx, current.ID, shipmentID)
	})
}

// releaseCancellationHold queues the release of a final request's hold.
func (uc *OrderUseCase) releaseCancellationHold(ctx context.Context, c *domain.CancellationRequest) error {
	if c.HoldStatus == nil || uc.Holds == nil {
		return nil
	}
	switch *c.HoldStatus {
	case domain.HoldPreparing, domain.HoldActive, domain.HoldNeedsReview:
	default:
		return nil
	}
	if _, err := uc.Cancellations.SetHold(ctx, c.ID, nil, []string{domain.HoldPreparing, domain.HoldActive, domain.HoldNeedsReview}, domain.HoldReleasing, nil); err != nil {
		return err
	}
	return uc.Effects.Enqueue(ctx, domain.Effect{OrderID: c.OrderID, Kind: domain.EffectReleaseSettlementHold, Target: cancellationTarget + c.ID})
}

// acquireCancellationHold is the acquire effect of a request: on Payment's
// answer the request leaves preparing (a hold Payment flagged for review
// still lets the admin decide; the note says why).
func (uc *OrderUseCase) acquireCancellationHold(ctx context.Context, id string) error {
	c, err := uc.Cancellations.FindByID(ctx, id)
	if err != nil {
		return appError(err)
	}
	if c.HoldID == nil || c.HoldStatus == nil || *c.HoldStatus != domain.HoldPreparing {
		return nil
	}
	receipt, err := uc.Holds.AcquireSettlementHold(ctx, adapter.HoldRequest{HoldID: *c.HoldID, VendorID: c.VendorID, VendorOrderID: c.VendorOrderID,
		SourceType: "paid_cancellation", SourceID: c.ID, SourceVersion: c.Version, ReasonCode: "paid_cancellation_" + c.Origin})
	to, note := domain.HoldActive, (*string)(nil)
	switch {
	case err == nil && receipt.Status == "released":
		to = domain.HoldReleased
	case err == nil:
	case isPermanent(err):
		message := appError(err).Message
		to, note = domain.HoldNeedsReview, &message
	default:
		return err
	}
	return uc.withOrder(ctx, c.OrderID, func(ctx context.Context) error {
		moved, err := uc.Cancellations.SetHold(ctx, c.ID, nil, []string{domain.HoldPreparing}, to, note)
		if err != nil || !moved {
			return err
		}
		current, err := uc.Cancellations.FindByID(ctx, c.ID)
		if err != nil {
			return err
		}
		if err := uc.Cancellations.AddEvent(ctx, &domain.CancellationEvent{RequestID: c.ID, ActorRole: "system", Action: "settlement_hold_" + to,
			ToStatus: string(current.Status), Note: note}); err != nil {
			return err
		}
		if current.Status != domain.CancelPreparing {
			return nil
		}
		return uc.moveCancellation(ctx, current, domain.CancelRequested, "system", nil, "hold_confirmed", note)
	})
}

// releaseCancellationHoldEffect is the release effect of a request.
func (uc *OrderUseCase) releaseCancellationHoldEffect(ctx context.Context, id string) error {
	c, err := uc.Cancellations.FindByID(ctx, id)
	if err != nil {
		return appError(err)
	}
	if c.HoldID == nil || c.HoldStatus == nil || *c.HoldStatus != domain.HoldReleasing {
		return nil
	}
	ref := string(c.Status)
	if c.RefundID != nil {
		ref += ":" + *c.RefundID
	}
	if _, err := uc.Holds.ReleaseSettlementHold(ctx, *c.HoldID, adapter.HoldRelease{OperationID: "cancellation_final:" + c.ID,
		SourceVersion: c.Version, ResolutionRef: ref, Reason: "Cancellation request " + string(c.Status)}); err != nil {
		return err
	}
	return uc.withOrder(ctx, c.OrderID, func(ctx context.Context) error {
		_, err := uc.Cancellations.SetHold(ctx, c.ID, nil, []string{domain.HoldReleasing}, domain.HoldReleased, nil)
		return err
	})
}

// reportCancellations logs requests waiting on someone.
func (uc *OrderUseCase) reportCancellations(ctx context.Context) {
	if uc.Cancellations == nil {
		return
	}
	open, review, stale, err := uc.Cancellations.Counts(ctx, uc.Now().Add(-time.Hour))
	if err != nil {
		if ctx.Err() == nil {
			uc.Log.Error().Err(err).Msg("order_cancellation_report_failed")
		}
		return
	}
	if open == 0 {
		return
	}
	levelFor(uc.Log, review > 0 || stale > 0).Int64("open", open).Int64("needs_review", review).
		Int64("stopping_or_refund_pending_over_1h", stale).Msg("order_cancellation_backlog")
}
