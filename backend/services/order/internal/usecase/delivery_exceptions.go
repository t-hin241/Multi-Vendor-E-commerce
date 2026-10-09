package usecase

import (
	"context"
	"errors"
	"strconv"
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

// DeliveryExceptionPort stores AF-04 cases, their timeline and receipts.
type DeliveryExceptionPort interface {
	Create(ctx context.Context, d *domain.DeliveryException) error
	FindByID(ctx context.Context, id string) (*domain.DeliveryException, error)
	FindOpen(ctx context.Context, vendorOrderID string) (*domain.DeliveryException, error)
	FindByShipment(ctx context.Context, shipmentID string) (*domain.DeliveryException, error)
	FindByRefund(ctx context.Context, refundID string) (*domain.DeliveryException, error)
	ListByOrder(ctx context.Context, orderID string) ([]*domain.DeliveryException, error)
	ListForVendor(ctx context.Context, vendorID, status string, limit, offset int) ([]*domain.DeliveryException, error)
	List(ctx context.Context, status string, limit, offset int) ([]*domain.DeliveryException, error)
	Save(ctx context.Context, d *domain.DeliveryException) error
	SetHold(ctx context.Context, id string, from []string, to string, note *string) (bool, error)
	AddEvent(ctx context.Context, e *domain.DeliveryExceptionEvent) error
	ListEvents(ctx context.Context, exceptionID string) ([]*domain.DeliveryExceptionEvent, error)
	AddReceipt(ctx context.Context, g *domain.GoodsReceipt) error
	LatestReceipt(ctx context.Context, exceptionID, shipmentID string) (*domain.GoodsReceipt, error)
	Counts(ctx context.Context, olderThan time.Time) (open, needsReview, stale, unowned int64, err error)
}

// ReplacementCreator is Shipment opening a redelivery attempt.
type ReplacementCreator interface {
	CreateReplacementAttempt(ctx context.Context, r adapter.ReplacementAttempt) (string, error)
}

const (
	notifyDeliveryExceptionOpened   = "delivery_exception_opened"
	notifyRedeliveryOffered         = "delivery_redelivery_offered"
	notifyDeliveryExceptionResolved = "delivery_exception_resolved"

	// deliveryTarget prefixes a hold effect's target for a case.
	deliveryTarget = "delivery_exception:"
)

var errDeliveryNotWired = errors.New("delivery exceptions are not wired")

func deliveryError(err error) error {
	switch {
	case errors.Is(err, repository.ErrDeliveryExceptionNotFound):
		return apperror.NotFound("Delivery exception not found")
	case errors.Is(err, repository.ErrStaleState):
		return domain.DeliveryExceptionChanged()
	}
	return asError(err)
}

// ShipmentExceptionFact is Shipment's report that delivery failed for good.
type ShipmentExceptionFact struct {
	EventID        string
	ShipmentID     string
	VendorOrderID  string
	Type           string
	AttemptNo      int
	FailedAttempts int
	Reason         string
	OccurredAt     time.Time
}

// ApplyShipmentException opens the vendor order's case (one open case per
// vendor order, one case per failing shipment) or adds the fact to the
// open one, in the caller's transaction (the event inbox's). A repeat
// changes nothing. Nothing is refunded or restocked here.
func (uc *OrderUseCase) ApplyShipmentException(ctx context.Context, f ShipmentExceptionFact) error {
	if uc.DeliveryExceptions == nil {
		return apperror.Internal(errDeliveryNotWired)
	}
	if !domain.ValidExceptionFact(f.Type) {
		return apperror.Validation("Unknown delivery exception type")
	}
	vo, err := uc.VendorOrders.FindByID(ctx, f.VendorOrderID)
	if err != nil {
		return notFoundOrInternal(err, repository.ErrVendorOrderNotFound, "Vendor order not found")
	}
	logger := uc.Log.With().Str("vendor_order_id", vo.ID).Str("shipment_id", f.ShipmentID).Str("exception_type", f.Type).Logger()
	var opened *domain.DeliveryException
	err = uc.withOrder(ctx, vo.OrderID, func(ctx context.Context) error {
		current, err := uc.VendorOrders.FindByID(ctx, vo.ID)
		if err != nil {
			return err
		}
		open, err := uc.DeliveryExceptions.FindOpen(ctx, current.ID)
		if err != nil {
			return err
		}
		if open != nil {
			return uc.applyFactToOpen(ctx, open, f)
		}
		if earlier, err := uc.DeliveryExceptions.FindByShipment(ctx, f.ShipmentID); err != nil || earlier != nil {
			return err // this shipment's case is already resolved: a late repeat
		}
		switch current.Status {
		case domain.StatusPaid, domain.StatusProcessing, domain.StatusShipped:
		default:
			// Refunded, cancelled or completed meanwhile (or a legacy
			// return already settled by hand): nothing left to resolve.
			logger.Warn().Str("vendor_order_status", string(current.Status)).Msg("order_delivery_exception_ignored")
			return nil
		}
		order, err := uc.findOrder(ctx, current.OrderID)
		if err != nil {
			return err
		}
		d := &domain.DeliveryException{OrderID: order.ID, VendorOrderID: current.ID, VendorID: current.VendorID, BuyerID: order.BuyerID,
			ShipmentID: f.ShipmentID, ExceptionType: f.Type, CurrentShipmentID: f.ShipmentID, AttemptNo: max(f.AttemptNo, 1),
			CarrierOutcome: f.Type, FailedAttempts: max(f.FailedAttempts, 0), DetectionReason: clipNote(f.Reason),
			Status: domain.StatusForFact(f.Type), Policy: domain.CurrentDeliveryPolicy()}
		ledger := uc.holdLedger()
		if ledger {
			holdID, preparing := uuid.NewString(), domain.HoldPreparing
			d.HoldID, d.HoldStatus = &holdID, &preparing
		}
		if err := uc.DeliveryExceptions.Create(ctx, d); err != nil {
			return err
		}
		if err := uc.DeliveryExceptions.AddEvent(ctx, &domain.DeliveryExceptionEvent{ExceptionID: d.ID, ActorRole: "system",
			Action: "detected_" + f.Type, ToStatus: string(d.Status), Note: d.DetectionReason}); err != nil {
			return err
		}
		if ledger {
			if err := uc.Effects.Enqueue(ctx, domain.Effect{OrderID: d.OrderID, Kind: domain.EffectAcquireSettlementHold,
				Target: deliveryTarget + d.ID}); err != nil {
				return err
			}
		}
		if err := uc.notifyDelivery(ctx, d, notifyDeliveryExceptionOpened, d.ID); err != nil {
			return err
		}
		if err := uc.noticeGoodsReturned(ctx, d, f.Type); err != nil {
			return err
		}
		opened = d
		return nil
	})
	if err != nil {
		logger.Warn().Err(err).Msg("order_delivery_exception_refused")
		return err
	}
	if opened != nil {
		logger.Warn().Str("exception_id", opened.ID).Str("status", string(opened.Status)).Msg("order_delivery_exception_opened")
		uc.runEffectsSoon(ctx, opened.OrderID)
	}
	return nil
}

// applyFactToOpen adds a later fact to the open case: the same attempt
// (attempts exhausted, then returned or lost) or the redelivery attempt
// failing too. A fact for another shipment is only noted.
func (uc *OrderUseCase) applyFactToOpen(ctx context.Context, d *domain.DeliveryException, f ShipmentExceptionFact) error {
	note := clipNote(f.Reason)
	switch {
	case f.ShipmentID == d.CurrentShipmentID:
		if d.CarrierOutcome == f.Type {
			return nil // a repeated fact
		}
	case d.ReplacementShipmentID != nil && f.ShipmentID == *d.ReplacementShipmentID:
		d.CurrentShipmentID, d.AttemptNo = f.ShipmentID, max(f.AttemptNo, d.AttemptNo+1)
	default:
		return uc.DeliveryExceptions.AddEvent(ctx, &domain.DeliveryExceptionEvent{ExceptionID: d.ID, ActorRole: "system",
			Action: "fact_other_shipment", ToStatus: string(d.Status), Note: ptr("Shipment " + f.ShipmentID + " reported " + f.Type)})
	}
	d.CarrierOutcome, d.FailedAttempts = f.Type, max(d.FailedAttempts, f.FailedAttempts)
	if err := uc.noticeGoodsReturned(ctx, d, f.Type); err != nil {
		return err
	}
	to := d.Status
	switch d.Status {
	case domain.DXInvestigating, domain.DXRedeliveryPending:
		to = domain.StatusForFact(f.Type)
	}
	return uc.moveDelivery(ctx, d, to, "system", nil, "carrier_"+f.Type, note)
}

// noteDeliveredForException is the delivered fact meeting an open case,
// under the order lock: the redelivery arrived (the case resolves), or the
// carrier delivered after all (the case goes to review; never settled by
// itself). skip: the vendor order must not move (already refunded).
func (uc *OrderUseCase) noteDeliveredForException(ctx context.Context, vo *domain.VendorOrder, shipmentID string) (skip bool, err error) {
	if uc.DeliveryExceptions == nil {
		return false, nil
	}
	d, err := uc.DeliveryExceptions.FindOpen(ctx, vo.ID)
	if err != nil || d == nil {
		return false, err
	}
	if d.ReplacementShipmentID != nil && shipmentID == *d.ReplacementShipmentID && d.Status == domain.DXRedeliveryPending {
		d.CurrentShipmentID, d.CarrierOutcome = shipmentID, domain.OutcomeDelivered
		if err := uc.moveDelivery(ctx, d, domain.DXResolved, "system", nil, "redelivered", nil); err != nil {
			return false, err
		}
		if err := uc.releaseDeliveryHold(ctx, d); err != nil {
			return false, err
		}
		uc.Log.Info().Str("exception_id", d.ID).Str("shipment_id", shipmentID).Msg("order_delivery_exception_redelivered")
		return false, uc.notifyDelivery(ctx, d, notifyDeliveryExceptionResolved, d.ID)
	}
	if shipmentID != d.CurrentShipmentID && shipmentID != d.ShipmentID {
		return false, nil
	}
	now := uc.Now().UTC()
	d.LateDeliveryAt, d.CarrierOutcome = &now, domain.OutcomeDelivered
	note := "The carrier reported the package delivered after the case opened; check before any refund or payout"
	d.ReviewReason = &note
	to := domain.DXNeedsReview
	if !domain.CanTransitionDeliveryException(d.Status, to) {
		to = d.Status
	}
	if err := uc.moveDelivery(ctx, d, to, "system", nil, "delivered_late", &note); err != nil {
		return false, err
	}
	uc.Log.Warn().Str("exception_id", d.ID).Str("shipment_id", shipmentID).Msg("order_delivery_exception_delivered_late")
	_, onPath := shipmentPaths["delivered"][vo.Status]
	return !onPath, nil
}

// moveDelivery applies one transition (or records a step in the same
// status) with its timeline entry.
func (uc *OrderUseCase) moveDelivery(ctx context.Context, d *domain.DeliveryException, to domain.DeliveryExceptionStatus, role string,
	actor *string, action string, note *string) error {
	if to != d.Status && !domain.CanTransitionDeliveryException(d.Status, to) {
		return apperror.Conflict("Cannot move this case from " + string(d.Status) + " to " + string(to))
	}
	from := d.Status
	d.Status = to
	if to == domain.DXResolved {
		now := uc.Now().UTC()
		d.ResolvedAt = &now
	}
	if err := uc.DeliveryExceptions.Save(ctx, d); err != nil {
		d.Status = from
		return err
	}
	fromStatus := string(from)
	return uc.DeliveryExceptions.AddEvent(ctx, &domain.DeliveryExceptionEvent{ExceptionID: d.ID, ActorID: actor, ActorRole: role, Action: action,
		FromStatus: &fromStatus, ToStatus: string(to), Note: note})
}

// noticeGoodsReturned tells the shop (PW-009) that a failed delivery is
// coming back and needs a goods receipt; once per case.
func (uc *OrderUseCase) noticeGoodsReturned(ctx context.Context, d *domain.DeliveryException, fact string) error {
	if fact != domain.FactReturned {
		return nil
	}
	return uc.noticeVendor(ctx, d.OrderID, d.VendorID, d.VendorOrderID, events.VendorActionDeliveryGoodsReturned, d.ID)
}

func (uc *OrderUseCase) notifyDelivery(ctx context.Context, d *domain.DeliveryException, kind, ref string) error {
	notice := domain.NewNotifyEffect(d.OrderID, d.BuyerID, kind)
	notice.Target = kind + ":" + ref
	return uc.Effects.Enqueue(ctx, notice)
}

func clipNote(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if len(s) > 500 {
		s = s[:500]
	}
	return &s
}

// DeliveryExceptionDetail is a case with what its caller may see.
type DeliveryExceptionDetail struct {
	Exception *domain.DeliveryException
	Events    []*domain.DeliveryExceptionEvent
}

// forBuyer hides the internal notes, the hold and the shop's receipt.
func forBuyer(d *domain.DeliveryException) *domain.DeliveryException {
	shown := *d
	shown.HoldID, shown.HoldStatus, shown.HoldNote, shown.ReviewReason, shown.DecidedBy, shown.Receipt = nil, nil, nil, nil, nil, nil
	shown.DetectionReason = nil
	return &shown
}

// forVendor hides the hold, internal notes and the buyer's new address
// (Shipment gives the shop the redelivery's destination).
func forVendor(d *domain.DeliveryException) *domain.DeliveryException {
	shown := *d
	shown.HoldID, shown.HoldStatus, shown.HoldNote, shown.ReviewReason, shown.DecidedBy, shown.RedeliveryAddress = nil, nil, nil, nil, nil, nil
	return &shown
}

// GetDeliveryException: the buyer of the order, the selling shop
// (orders.read) or an admin.
func (uc *OrderUseCase) GetDeliveryException(ctx context.Context, actor SupportActor, id string) (*DeliveryExceptionDetail, error) {
	if uc.DeliveryExceptions == nil {
		return nil, apperror.NotFound("Delivery exception not found")
	}
	d, err := uc.DeliveryExceptions.FindByID(ctx, id)
	if err != nil {
		return nil, deliveryError(err)
	}
	if err := uc.canSeeDelivery(ctx, actor, d); err != nil {
		return nil, err
	}
	if d.Receipt, err = uc.DeliveryExceptions.LatestReceipt(ctx, d.ID, d.CurrentShipmentID); err != nil {
		return nil, appError(err)
	}
	events, err := uc.DeliveryExceptions.ListEvents(ctx, d.ID)
	if err != nil {
		return nil, appError(err)
	}
	if actor.Role != "admin" {
		visible := make([]*domain.DeliveryExceptionEvent, 0, len(events))
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
		if actor.Role == "buyer" {
			d = forBuyer(d)
		} else {
			d = forVendor(d)
		}
	}
	return &DeliveryExceptionDetail{Exception: d, Events: events}, nil
}

func (uc *OrderUseCase) canSeeDelivery(ctx context.Context, actor SupportActor, d *domain.DeliveryException) error {
	switch actor.Role {
	case "buyer":
		if d.BuyerID != actor.ID {
			return apperror.NotFound("Delivery exception not found")
		}
	case "vendor":
		if _, err := uc.Vendors.GetApprovedVendorID(ctx, actor.ID, d.VendorID, shopaccess.OrdersRead); err != nil {
			if shopaccess.IsUnavailable(err) {
				return err
			}
			return apperror.NotFound("Delivery exception not found")
		}
	case "admin":
	default:
		return apperror.NotFound("Delivery exception not found")
	}
	return nil
}

// ListOrderDeliveryExceptions lists the buyer's cases on one order.
func (uc *OrderUseCase) ListOrderDeliveryExceptions(ctx context.Context, buyerID, orderID string) ([]*domain.DeliveryException, error) {
	if uc.DeliveryExceptions == nil {
		return []*domain.DeliveryException{}, nil
	}
	order, err := uc.findOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order.BuyerID != buyerID {
		return nil, apperror.NotFound("Order not found")
	}
	out, err := uc.DeliveryExceptions.ListByOrder(ctx, orderID)
	if err != nil {
		return nil, appError(err)
	}
	for i, d := range out {
		out[i] = forBuyer(d)
	}
	return out, nil
}

func (uc *OrderUseCase) ListVendorDeliveryExceptions(ctx context.Context, userID, vendorID, status string, limit, offset int) ([]*domain.DeliveryException, error) {
	if uc.DeliveryExceptions == nil {
		return []*domain.DeliveryException{}, nil
	}
	vendorID, err := uc.Vendors.GetApprovedVendorID(ctx, userID, vendorID, shopaccess.OrdersRead)
	if err != nil {
		return nil, err
	}
	out, err := uc.DeliveryExceptions.ListForVendor(ctx, vendorID, status, limit, offset)
	if err != nil {
		return nil, deliveryError(err)
	}
	for i, d := range out {
		out[i] = forVendor(d)
	}
	return out, nil
}

func (uc *OrderUseCase) ListDeliveryExceptions(ctx context.Context, status string, limit, offset int) ([]*domain.DeliveryException, error) {
	if uc.DeliveryExceptions == nil {
		return []*domain.DeliveryException{}, nil
	}
	out, err := uc.DeliveryExceptions.List(ctx, status, limit, offset)
	return out, deliveryError(err)
}

// ReceiptInput is what the shop (or an admin correcting it) received back.
type ReceiptInput struct {
	Lines           []domain.ReceiptLine
	Note            string
	ExpectedVersion int64
}

// RecordGoodsReceipt records what came back for the current attempt: every
// unit sellable, damaged or missing. The shop records it once; an admin
// may correct it (a new version, with a reason) until stock is put back.
func (uc *OrderUseCase) RecordGoodsReceipt(ctx context.Context, actor SupportActor, id string, in ReceiptInput) (*domain.DeliveryException, error) {
	if uc.DeliveryExceptions == nil {
		return nil, apperror.NotFound("Delivery exception not found")
	}
	note, err := domain.ValidateNote(in.Note, 1000, actor.Role == "admin", "A note")
	if err != nil {
		return nil, err
	}
	first, err := uc.DeliveryExceptions.FindByID(ctx, id)
	if err != nil {
		return nil, deliveryError(err)
	}
	switch actor.Role {
	case "vendor":
		if _, err := uc.Vendors.GetApprovedVendorID(ctx, actor.ID, first.VendorID, shopaccess.ReturnsHandle); err != nil {
			if shopaccess.IsUnavailable(err) {
				return nil, err
			}
			return nil, apperror.NotFound("Delivery exception not found")
		}
	case "admin":
		if err := uc.requireAdmin(ctx, actor.ID); err != nil {
			return nil, err
		}
	default:
		return nil, apperror.NotFound("Delivery exception not found")
	}
	var result *domain.DeliveryException
	err = uc.withOrder(ctx, first.OrderID, func(ctx context.Context) error {
		d, err := uc.DeliveryExceptions.FindByID(ctx, id)
		if err != nil {
			return err
		}
		if d.Version != in.ExpectedVersion {
			return domain.DeliveryExceptionChanged()
		}
		switch {
		case !d.Status.Open():
			return apperror.Conflict("This case is resolved")
		case !d.GoodsBack():
			return apperror.Conflict("The package has not come back to the shop")
		case d.RecoveryRef != nil:
			return apperror.Conflict("Stock was already put back; the receipt can no longer change")
		}
		latest, err := uc.DeliveryExceptions.LatestReceipt(ctx, d.ID, d.CurrentShipmentID)
		if err != nil {
			return err
		}
		version, action := 1, "goods_received"
		if latest != nil {
			if actor.Role != "admin" {
				return domain.ReceiptExists()
			}
			version, action = latest.Version+1, "receipt_corrected"
		}
		items, err := uc.Orders.ListItemsByOrder(ctx, d.OrderID)
		if err != nil {
			return err
		}
		ordered := map[string]int64{}
		for _, item := range items {
			if item.VendorOrderID == d.VendorOrderID {
				ordered[item.ID] = item.Quantity
			}
		}
		if err := domain.ValidateReceipt(in.Lines, ordered); err != nil {
			return err
		}
		g := &domain.GoodsReceipt{ExceptionID: d.ID, ShipmentID: d.CurrentShipmentID, Version: version, RecordedBy: actor.ID,
			ActorRole: actor.Role, Note: note, Lines: in.Lines}
		if err := uc.DeliveryExceptions.AddReceipt(ctx, g); err != nil {
			return err
		}
		d.Receipt = g
		if actor.Role == "admin" {
			if err := uc.audit(ctx, domain.AdminAction{ActorID: actor.ID, Action: "delivery_" + action, EntityType: domain.AuditDeliveryException,
				EntityID: d.ID, OrderID: &d.OrderID, Reason: note, Changes: map[string]any{"receipt_version": version}}); err != nil {
				return err
			}
		}
		if err := uc.moveDelivery(ctx, d, d.Status, actor.Role, &actor.ID, action, note); err != nil {
			return err
		}
		// The buyer was already refunded and only the goods were missing.
		if d.Status == domain.DXAwaitingGoods && d.Resolution != nil && *d.Resolution == domain.ResolutionRefundDX && d.RefundID != nil {
			if err := uc.finishDelivery(ctx, d, "goods_received_after_refund"); err != nil {
				return err
			}
		}
		result = d
		return nil
	})
	if err != nil {
		return nil, deliveryError(err)
	}
	uc.Log.Info().Str("exception_id", id).Str("actor_role", actor.Role).Bool("all_sellable", result.Receipt.AllSellable()).
		Msg("order_delivery_goods_received")
	uc.runEffectsSoon(ctx, result.OrderID)
	return result, nil
}

// DeliveryDecision is an admin's decision on a case.
type DeliveryDecision struct {
	Resolution      string // redeliver, refund, close, retry_refund
	Reason          string
	ExpectedVersion int64
}

// DecideDeliveryException offers a redelivery (the buyer then confirms
// the address), refunds the buyer, closes a case the carrier delivered
// after all, or retries a failed refund. A refund and a redelivery are
// exclusive: the first committed under the order lock wins.
func (uc *OrderUseCase) DecideDeliveryException(ctx context.Context, adminID, id string, in DeliveryDecision) (*domain.DeliveryException, error) {
	if uc.DeliveryExceptions == nil {
		return nil, apperror.NotFound("Delivery exception not found")
	}
	reason, err := adminReason(in.Reason)
	if err != nil {
		return nil, err
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	first, err := uc.DeliveryExceptions.FindByID(ctx, id)
	if err != nil {
		return nil, deliveryError(err)
	}
	var result *domain.DeliveryException
	err = uc.withOrder(ctx, first.OrderID, func(ctx context.Context) error {
		d, err := uc.DeliveryExceptions.FindByID(ctx, id)
		if err != nil {
			return err
		}
		if d.Version != in.ExpectedVersion {
			return domain.DeliveryExceptionChanged()
		}
		if !d.Status.Open() {
			return apperror.Conflict("This case is resolved")
		}
		now := uc.Now().UTC()
		decided := func() { d.DecidedBy, d.DecidedAt, d.DecisionReason = &adminID, &now, reason }
		switch in.Resolution {
		case "redeliver":
			if err := uc.checkRedelivery(ctx, d); err != nil {
				return err
			}
			decided()
			d.Resolution, d.ReviewReason = ptr(domain.ResolutionRedelivery), nil
			if err := uc.moveDelivery(ctx, d, domain.DXAwaitingBuyer, "admin", &adminID, "redelivery_offered", reason); err != nil {
				return err
			}
			if err := uc.notifyDelivery(ctx, d, notifyRedeliveryOffered, d.ID+":"+strconv.Itoa(d.RedeliveryCount+1)); err != nil {
				return err
			}
		case "refund":
			if d.RefundID != nil {
				return apperror.Conflict("A refund was already requested for this case; retry it instead")
			}
			switch d.Status {
			case domain.DXInvestigating, domain.DXAwaitingGoods, domain.DXAwaitingBuyer, domain.DXNeedsReview:
			default:
				return apperror.Conflict("This case cannot be refunded now")
			}
			if !d.CarrierFinal() {
				return apperror.Conflict("The package may still reach the buyer; wait until it is returned or the carrier confirms it lost")
			}
			if err := uc.requireDeliveryHold(d); err != nil {
				return err
			}
			decided()
			d.Resolution = ptr(domain.ResolutionRefundDX)
			if err := uc.requestDeliveryRefund(ctx, d, adminID, reason); err != nil {
				return err
			}
		case "close":
			if d.LateDeliveryAt == nil || d.CarrierOutcome != domain.OutcomeDelivered {
				return apperror.Conflict("Only a case the carrier delivered after all can be closed without redelivery or refund")
			}
			if d.Status == domain.DXRefundPending {
				return apperror.Conflict("Wait for the refund's outcome before closing")
			}
			if d.RefundID != nil {
				refund, err := uc.Refunds.FindByID(ctx, *d.RefundID)
				if err != nil {
					return err
				}
				if !refund.Status.Terminal() {
					return apperror.Conflict("Wait for the refund's outcome before closing")
				}
			}
			decided()
			if d.Resolution == nil {
				d.Resolution = ptr(domain.ResolutionClosed)
			}
			if err := uc.moveDelivery(ctx, d, domain.DXResolved, "admin", &adminID, "closed_delivered", reason); err != nil {
				return err
			}
			if err := uc.releaseDeliveryHold(ctx, d); err != nil {
				return err
			}
			if err := uc.notifyDelivery(ctx, d, notifyDeliveryExceptionResolved, d.ID); err != nil {
				return err
			}
		case "retry_refund":
			if d.Status != domain.DXNeedsReview || d.RefundID == nil || d.LateDeliveryAt != nil {
				return apperror.Conflict("Only a case whose refund failed can retry it")
			}
			refund, err := uc.Refunds.FindByID(ctx, *d.RefundID)
			if err != nil {
				return err
			}
			if refund.Status != domain.RefundFailed && refund.Status != domain.RefundRejected {
				return apperror.Conflict("The refund has not failed")
			}
			decided()
			if err := uc.requestDeliveryRefund(ctx, d, adminID, reason); err != nil {
				return err
			}
		default:
			return apperror.Validation("resolution must be redeliver, refund, close or retry_refund")
		}
		result = d
		return uc.audit(ctx, domain.AdminAction{ActorID: adminID, Action: "delivery_" + in.Resolution, EntityType: domain.AuditDeliveryException,
			EntityID: d.ID, OrderID: &d.OrderID, Reason: reason, Changes: map[string]any{"status": string(d.Status), "resolution": d.Resolution}})
	})
	if err != nil {
		return nil, deliveryError(err)
	}
	uc.Log.Info().Str("exception_id", id).Str("resolution", in.Resolution).Str("status", string(result.Status)).Msg("order_delivery_exception_decided")
	uc.runEffectsSoon(ctx, result.OrderID)
	return result, nil
}

// checkRedelivery: the goods are back at the shop, all sellable, the
// refund branch was not chosen, the limit is not reached and the payout is
// held. Only with FEATURE_DELIVERY_RESOLUTION_ENABLED.
func (uc *OrderUseCase) checkRedelivery(ctx context.Context, d *domain.DeliveryException) error {
	switch {
	case !uc.DeliveryRedelivery:
		return domain.RedeliveryDisabled()
	case d.Resolution != nil && *d.Resolution == domain.ResolutionRefundDX:
		return domain.ResolutionLocked()
	case d.Status != domain.DXAwaitingGoods && d.Status != domain.DXNeedsReview:
		return apperror.Conflict("A redelivery is offered once the package is back at the shop")
	case !d.GoodsBack():
		return apperror.Conflict("A redelivery is offered once the package is back at the shop")
	case d.RedeliveryCount >= d.Policy.RedeliveryLimit:
		return apperror.Conflict("This package was already redelivered once; refund the buyer instead")
	}
	if err := uc.requireDeliveryHold(d); err != nil {
		return err
	}
	receipt, err := uc.DeliveryExceptions.LatestReceipt(ctx, d.ID, d.CurrentShipmentID)
	if err != nil {
		return err
	}
	if receipt == nil {
		return apperror.Conflict("The shop must record what came back before a redelivery")
	}
	if !receipt.AllSellable() {
		return apperror.Conflict("Only goods that came back sellable can be redelivered; refund the buyer instead")
	}
	return nil
}

// requireDeliveryHold: money decisions wait for Payment's hold (a hold
// Payment flagged for review still counts: the buyer is owed anyway).
func (uc *OrderUseCase) requireDeliveryHold(d *domain.DeliveryException) error {
	if d.HoldStatus != nil && *d.HoldStatus == domain.HoldPreparing {
		return domain.HoldUnavailable()
	}
	return nil
}

// requestDeliveryRefund refunds what is left of the vendor order: the
// merchandise and the shipping fee paid (the package never reached the
// buyer, delivery-resolution-v1).
func (uc *OrderUseCase) requestDeliveryRefund(ctx context.Context, d *domain.DeliveryException, actorID string, note *string) error {
	order, err := uc.findOrder(ctx, d.OrderID)
	if err != nil {
		return err
	}
	vo, err := uc.VendorOrders.FindByID(ctx, d.VendorOrderID)
	if err != nil {
		return err
	}
	amount, err := uc.refundableFor(ctx, order, vo)
	if err != nil {
		return err
	}
	if amount <= 0 {
		return uc.afterDeliveryRefund(ctx, d, "nothing_to_refund", note)
	}
	refund := &domain.Refund{OrderID: order.ID, VendorOrderID: &vo.ID, ReasonCode: domain.RefundReasonDeliveryException, Amount: amount,
		Currency: order.Currency, Reason: "Delivery exception " + d.ID + " (" + d.CarrierOutcome + ")", RequestedBy: actorID}
	if err := uc.createRefund(ctx, refund); err != nil {
		return err
	}
	d.RefundID, d.ReviewReason = &refund.ID, nil
	return uc.moveDelivery(ctx, d, domain.DXRefundPending, "admin", &actorID, "refund_requested", note)
}

// afterDeliveryRefund: the buyer has their money; the case ends once the
// goods that came back are recorded (lost goods never come back).
func (uc *OrderUseCase) afterDeliveryRefund(ctx context.Context, d *domain.DeliveryException, action string, note *string) error {
	if d.GoodsBack() {
		receipt, err := uc.DeliveryExceptions.LatestReceipt(ctx, d.ID, d.CurrentShipmentID)
		if err != nil {
			return err
		}
		if receipt == nil {
			return uc.moveDelivery(ctx, d, domain.DXAwaitingGoods, "system", nil, action+"_awaiting_goods", note)
		}
		d.Receipt = receipt
	}
	return uc.finishDelivery(ctx, d, action)
}

// finishDelivery resolves a refunded case: sellable units go back to
// stock once (effect), the hold is released, the buyer is told.
func (uc *OrderUseCase) finishDelivery(ctx context.Context, d *domain.DeliveryException, action string) error {
	if d.Receipt != nil && len(d.Receipt.SellableByItem()) > 0 {
		ref := deliveryTarget + d.ID
		d.RecoveryRef = &ref
		if err := uc.Effects.Enqueue(ctx, domain.Effect{OrderID: d.OrderID, Kind: domain.EffectRecoverDeliveryStock, Target: d.ID}); err != nil {
			return err
		}
	}
	if err := uc.moveDelivery(ctx, d, domain.DXResolved, "system", nil, action, nil); err != nil {
		return err
	}
	if err := uc.releaseDeliveryHold(ctx, d); err != nil {
		return err
	}
	return uc.notifyDelivery(ctx, d, notifyDeliveryExceptionResolved, d.ID)
}

// syncDeliveryRefund follows a case's refund outcome, in the outcome's
// transaction under the order lock.
func (uc *OrderUseCase) syncDeliveryRefund(ctx context.Context, refund *domain.Refund, succeeded bool, note *string) error {
	if uc.DeliveryExceptions == nil {
		return nil
	}
	found, err := uc.DeliveryExceptions.FindByRefund(ctx, refund.ID)
	if err != nil || found == nil {
		return err
	}
	d, err := uc.DeliveryExceptions.FindByID(ctx, found.ID)
	if err != nil {
		return err
	}
	switch {
	case d.Status == domain.DXRefundPending && succeeded:
		return uc.afterDeliveryRefund(ctx, d, "refund_succeeded", nil)
	case d.Status == domain.DXRefundPending:
		d.ReviewReason = note
		uc.Log.Warn().Str("exception_id", d.ID).Str("refund_id", refund.ID).Msg("order_delivery_exception_refund_failed")
		return uc.moveDelivery(ctx, d, domain.DXNeedsReview, "system", nil, "refund_failed", note)
	case d.Status.Open():
		// In review (delivered late meanwhile): keep the outcome on record.
		action := "refund_failed"
		if succeeded {
			action = "refund_succeeded"
		}
		return uc.moveDelivery(ctx, d, d.Status, "system", nil, action, note)
	}
	return nil
}

// ConsentInput is the buyer's answer to a redelivery offer.
type ConsentInput struct {
	Accept          bool
	AddressID       string
	ExpectedVersion int64
}

// ConsentRedelivery: the buyer accepts the redelivery to the order's
// address or one of their saved addresses (no new charge), or declines
// (an admin then refunds). Accepting asks Shipment for the new attempt.
func (uc *OrderUseCase) ConsentRedelivery(ctx context.Context, buyerID, id string, in ConsentInput) (*domain.DeliveryException, error) {
	if uc.DeliveryExceptions == nil {
		return nil, apperror.NotFound("Delivery exception not found")
	}
	first, err := uc.DeliveryExceptions.FindByID(ctx, id)
	if err != nil {
		return nil, deliveryError(err)
	}
	if first.BuyerID != buyerID {
		return nil, apperror.NotFound("Delivery exception not found")
	}
	var address *domain.BuyerAddress
	if in.Accept && in.AddressID != "" {
		if address, err = uc.resolveCheckoutAddress(ctx, buyerID, in.AddressID); err != nil {
			return nil, err
		}
	}
	var result *domain.DeliveryException
	err = uc.withOrder(ctx, first.OrderID, func(ctx context.Context) error {
		d, err := uc.DeliveryExceptions.FindByID(ctx, id)
		if err != nil {
			return err
		}
		if d.Version != in.ExpectedVersion {
			return domain.DeliveryExceptionChanged()
		}
		if d.Status != domain.DXAwaitingBuyer {
			return apperror.Conflict("No redelivery is waiting for your answer")
		}
		if !in.Accept {
			note := "The buyer declined the redelivery"
			d.ReviewReason = &note
			if err := uc.moveDelivery(ctx, d, domain.DXNeedsReview, "buyer", &buyerID, "redelivery_declined", nil); err != nil {
				return err
			}
			result = d
			return nil
		}
		dest := domain.Destination{}
		if address != nil {
			dest = domain.Destination{RecipientName: address.RecipientName, Phone: address.Phone, Province: address.Province,
				District: address.District, Ward: address.Ward, StreetAddress: address.StreetAddress, AddressID: &address.ID}
		} else {
			order, err := uc.findOrder(ctx, d.OrderID)
			if err != nil {
				return err
			}
			dest = domain.Destination{RecipientName: order.RecipientName, Phone: order.Phone, Province: order.Province,
				District: order.District, Ward: order.Ward, StreetAddress: order.StreetAddress}
		}
		now := uc.Now().UTC()
		d.RedeliveryAddress, d.ConsentedAt = &dest, &now
		d.RedeliveryCount++
		d.ReplacementShipmentID = nil
		if err := uc.moveDelivery(ctx, d, domain.DXRedeliveryPending, "buyer", &buyerID, "redelivery_accepted", nil); err != nil {
			return err
		}
		// PW-009: the shop prepares the redelivery.
		if err := uc.noticeVendor(ctx, d.OrderID, d.VendorID, d.VendorOrderID, events.VendorActionRedeliveryAccepted, d.ID); err != nil {
			return err
		}
		if err := uc.Effects.Enqueue(ctx, domain.Effect{OrderID: d.OrderID, Kind: domain.EffectCreateReplacementAttempt,
			Target: d.ID + ":" + strconv.Itoa(d.RedeliveryCount)}); err != nil {
			return err
		}
		result = d
		return nil
	})
	if err != nil {
		return nil, deliveryError(err)
	}
	uc.Log.Info().Str("exception_id", id).Bool("accepted", in.Accept).Bool("new_address", address != nil).Msg("order_delivery_redelivery_answered")
	uc.runEffectsSoon(ctx, result.OrderID)
	return forBuyer(result), nil
}

// createReplacementAttempt is the redelivery effect: ask Shipment for the
// next attempt (once per operation), then record it. A refusal hands the
// case to an admin.
func (uc *OrderUseCase) createReplacementAttempt(ctx context.Context, e *domain.Effect) error {
	if uc.DeliveryExceptions == nil || uc.Replacements == nil {
		return errDeliveryNotWired
	}
	id, n, _ := strings.Cut(e.Target, ":")
	d, err := uc.DeliveryExceptions.FindByID(ctx, id)
	if err != nil {
		return appError(err)
	}
	if d.Status != domain.DXRedeliveryPending || d.ReplacementShipmentID != nil || d.RedeliveryAddress == nil ||
		strconv.Itoa(d.RedeliveryCount) != n {
		return nil
	}
	shipmentID, callErr := uc.Replacements.CreateReplacementAttempt(ctx, adapter.ReplacementAttempt{
		OperationID: deliveryTarget + d.ID + ":redelivery:" + n, VendorOrderID: d.VendorOrderID, OriginalShipmentID: d.CurrentShipmentID,
		AttemptNo: d.AttemptNo + 1, EligibilityRef: d.ID, Destination: *d.RedeliveryAddress})
	if callErr != nil && !isPermanent(callErr) {
		return callErr
	}
	return uc.withOrder(ctx, d.OrderID, func(ctx context.Context) error {
		d, err := uc.DeliveryExceptions.FindByID(ctx, id)
		if err != nil || d.Status != domain.DXRedeliveryPending || d.ReplacementShipmentID != nil {
			return err
		}
		if callErr != nil {
			note := "Shipment refused the redelivery: " + appError(callErr).Message
			if len(note) > 500 {
				note = note[:500]
			}
			uc.Log.Warn().Str("exception_id", d.ID).Msg("order_delivery_redelivery_refused")
			d.ReviewReason = &note
			return uc.moveDelivery(ctx, d, domain.DXNeedsReview, "system", nil, "redelivery_refused", &note)
		}
		d.ReplacementShipmentID = &shipmentID
		uc.Log.Info().Str("exception_id", d.ID).Str("shipment_id", shipmentID).Msg("order_delivery_redelivery_created")
		return uc.moveDelivery(ctx, d, d.Status, "system", nil, "redelivery_created", nil)
	})
}

// recoverDeliveryStock is the restock effect: the sellable units of the
// latest receipt go back once per item.
func (uc *OrderUseCase) recoverDeliveryStock(ctx context.Context, e *domain.Effect) error {
	if uc.DeliveryExceptions == nil || uc.Recoveries == nil {
		return errDeliveryNotWired
	}
	d, err := uc.DeliveryExceptions.FindByID(ctx, e.Target)
	if err != nil {
		return appError(err)
	}
	if d.RecoveryRef == nil {
		return nil
	}
	receipt, err := uc.DeliveryExceptions.LatestReceipt(ctx, d.ID, d.CurrentShipmentID)
	if err != nil || receipt == nil {
		return asError(err)
	}
	sellable := receipt.SellableByItem()
	items, err := uc.Orders.ListItemsByOrder(ctx, d.OrderID)
	if err != nil {
		return appError(err)
	}
	restocked := int64(0)
	for _, item := range items {
		qty := sellable[item.ID]
		if item.VendorOrderID != d.VendorOrderID || qty <= 0 {
			continue
		}
		if err := uc.Recoveries.RestockRecovery(ctx, domain.DeliveryRecoveryID(d.ID, item.ID), item.ProductID, item.VariantID, qty); err != nil {
			return err
		}
		restocked += qty
	}
	uc.Log.Info().Str("exception_id", d.ID).Int64("units", restocked).Msg("order_delivery_stock_recovered")
	return nil
}

// deliveryHandoverAllowed answers the handover fence for a vendor order
// with an open case: only its recorded redelivery attempt may ship.
// handled false: no open case, the usual rules apply.
func (uc *OrderUseCase) deliveryHandoverAllowed(ctx context.Context, vendorOrderID, shipmentID string) (handled bool, err error) {
	if uc.DeliveryExceptions == nil {
		return false, nil
	}
	d, err := uc.DeliveryExceptions.FindOpen(ctx, vendorOrderID)
	if err != nil || d == nil {
		return false, err
	}
	if d.Status == domain.DXRedeliveryPending && d.ReplacementShipmentID != nil && *d.ReplacementShipmentID == shipmentID {
		return true, nil
	}
	return true, domain.DeliveryExceptionOpen()
}

// releaseDeliveryHold queues the release of a resolved case's hold.
func (uc *OrderUseCase) releaseDeliveryHold(ctx context.Context, d *domain.DeliveryException) error {
	if d.HoldStatus == nil || uc.Holds == nil {
		return nil
	}
	switch *d.HoldStatus {
	case domain.HoldPreparing, domain.HoldActive, domain.HoldNeedsReview:
	default:
		return nil
	}
	if _, err := uc.DeliveryExceptions.SetHold(ctx, d.ID, []string{domain.HoldPreparing, domain.HoldActive, domain.HoldNeedsReview}, domain.HoldReleasing, nil); err != nil {
		return err
	}
	releasing := domain.HoldReleasing
	d.HoldStatus = &releasing
	return uc.Effects.Enqueue(ctx, domain.Effect{OrderID: d.OrderID, Kind: domain.EffectReleaseSettlementHold, Target: deliveryTarget + d.ID})
}

// acquireDeliveryHold is the acquire effect of a case.
func (uc *OrderUseCase) acquireDeliveryHold(ctx context.Context, id string) error {
	d, err := uc.DeliveryExceptions.FindByID(ctx, id)
	if err != nil {
		return appError(err)
	}
	if d.HoldID == nil || d.HoldStatus == nil || *d.HoldStatus != domain.HoldPreparing {
		return nil
	}
	receipt, err := uc.Holds.AcquireSettlementHold(ctx, adapter.HoldRequest{HoldID: *d.HoldID, VendorID: d.VendorID, VendorOrderID: d.VendorOrderID,
		SourceType: "delivery_exception", SourceID: d.ID, SourceVersion: d.Version, ReasonCode: "delivery_" + d.ExceptionType})
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
	return uc.withOrder(ctx, d.OrderID, func(ctx context.Context) error {
		moved, err := uc.DeliveryExceptions.SetHold(ctx, d.ID, []string{domain.HoldPreparing}, to, note)
		if err != nil || !moved {
			return err
		}
		current, err := uc.DeliveryExceptions.FindByID(ctx, d.ID)
		if err != nil {
			return err
		}
		return uc.DeliveryExceptions.AddEvent(ctx, &domain.DeliveryExceptionEvent{ExceptionID: d.ID, ActorRole: "system",
			Action: "settlement_hold_" + to, ToStatus: string(current.Status), Note: note})
	})
}

// releaseDeliveryHoldEffect is the release effect of a case.
func (uc *OrderUseCase) releaseDeliveryHoldEffect(ctx context.Context, id string) error {
	d, err := uc.DeliveryExceptions.FindByID(ctx, id)
	if err != nil {
		return appError(err)
	}
	if d.HoldID == nil || d.HoldStatus == nil || *d.HoldStatus != domain.HoldReleasing {
		return nil
	}
	ref := string(d.Status)
	if d.Resolution != nil {
		ref += ":" + *d.Resolution
	}
	if d.RefundID != nil {
		ref += ":" + *d.RefundID
	}
	if _, err := uc.Holds.ReleaseSettlementHold(ctx, *d.HoldID, adapter.HoldRelease{OperationID: "delivery_exception_final:" + d.ID,
		SourceVersion: d.Version, ResolutionRef: ref, Reason: "Delivery exception " + string(d.Status)}); err != nil {
		return err
	}
	return uc.withOrder(ctx, d.OrderID, func(ctx context.Context) error {
		_, err := uc.DeliveryExceptions.SetHold(ctx, d.ID, []string{domain.HoldReleasing}, domain.HoldReleased, nil)
		return err
	})
}

// reportDeliveryExceptions logs cases waiting on someone.
func (uc *OrderUseCase) reportDeliveryExceptions(ctx context.Context) {
	if uc.DeliveryExceptions == nil {
		return
	}
	open, review, stale, unowned, err := uc.DeliveryExceptions.Counts(ctx, uc.Now().Add(-24*time.Hour))
	if err != nil {
		if ctx.Err() == nil {
			uc.Log.Error().Err(err).Msg("order_delivery_exception_report_failed")
		}
		return
	}
	if open == 0 {
		return
	}
	levelFor(uc.Log, review > 0 || stale > 0 || unowned > 0).Int64("open", open).Int64("needs_review", review).
		Int64("redelivery_or_refund_pending_over_24h", stale).Int64("investigating_undecided_over_24h", unowned).
		Msg("order_delivery_exception_backlog")
}
