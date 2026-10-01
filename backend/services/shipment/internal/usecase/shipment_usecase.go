// Package usecase orchestrates Shipment's workflows: quoting, opening a
// vendor order's shipment once Order released it, fulfillment actions with
// an audited timeline, carrier interception, and telling Order what
// happened through an outbox. Shipment never writes Order's data; Order
// decides what shipped/delivered/returned mean for the vendor order.
package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/carrier"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
)

type Deps struct {
	Tx            Transactor
	Shipments     ShipmentRepositoryPort
	VendorMethods VendorShippingMethodRepositoryPort
	Zones         ZoneRepositoryPort
	FeeRules      FeeRuleRepositoryPort
	Events        TrackingEventRepositoryPort
	Outbox        OutboxPort
	Vendors       VendorGateway
	Orders        OrderGateway
	Identity      RoleVerifier
	Audit         AuditPort
	Carrier       carrier.Provider
	Verifier      carrier.Verifier
	// Simulator is set only with the mock carrier (never in production).
	Simulator CarrierSimulator
	// Wake asks the outbox worker to deliver now.
	Wake func()
	Log  zerolog.Logger
	Now  func() time.Time
}

type ShipmentUseCase struct{ Deps }

func NewShipmentUseCase(d Deps) *ShipmentUseCase {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &ShipmentUseCase{d}
}

// Actor is who performs a fulfillment action.
type Actor struct {
	ID   string
	Role domain.ActorRole
}

// notShippableStatuses are vendor-order statuses that never get a shipment.
var notShippableStatuses = map[string]bool{"cancelled": true, "refunded": true}

// CreateShipmentInput carries what Order knows about a paid vendor order.
// Shipment trusts it over the service-authenticated internal boundary.
type CreateShipmentInput struct {
	VendorOrderID      string
	VendorID           string
	BuyerID            string
	PackageWeightGrams int64
	RecipientName      string
	Phone              string
	Province           string
	District           string
	Ward               string
	StreetAddress      string
	// Quote, when set, is the fee Order snapshotted at checkout.
	Quote *domain.QuotedFee
}

// Quote prices a vendor's package to a destination without creating a
// shipment. A shop without a shipping method, a destination outside every
// zone, a missing fee rule or a missing weight is unavailable, never free.
func (uc *ShipmentUseCase) Quote(ctx context.Context, vendorID, province string, weightGrams int64) (*domain.Quote, error) {
	if err := domain.ValidateQuoteInput(vendorID, province, weightGrams); err != nil {
		uc.Log.Info().Str("vendor_id", vendorID).Str("reason", "invalid_input").Msg("shipment_quote_unavailable")
		return nil, err
	}
	unavailable := func(reason, message string) error {
		uc.Log.Info().Str("vendor_id", vendorID).Str("reason", reason).Msg("shipment_quote_unavailable")
		return apperror.Validation(message)
	}
	method, err := uc.VendorMethods.FindDefaultForVendor(ctx, vendorID)
	if errors.Is(err, repository.ErrVendorShippingMethodNotFound) {
		return nil, unavailable("no_shipping_method", "This shop has not set up a shipping method yet")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	zone, err := uc.Zones.FindZoneByProvinceCode(ctx, province)
	if errors.Is(err, repository.ErrZoneNotFound) {
		return nil, unavailable("no_zone", "Shipping is not available for this destination yet")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	rule, err := uc.FeeRules.FindCurrent(ctx, method.CarrierID, zone.ID)
	if errors.Is(err, repository.ErrFeeRuleNotFound) {
		return nil, unavailable("no_fee_rule", "Shipping fee is not configured for this destination yet")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	now := uc.Now().UTC()
	return &domain.Quote{
		VendorID: vendorID, FeeAmount: domain.ComputeShippingFee(*rule, weightGrams), Currency: domain.FeeCurrency,
		CarrierID: method.CarrierID, ZoneID: zone.ID, ZoneName: zone.Name, FeeRuleID: rule.ID, FeeRuleVersion: rule.Version,
		PackageWeightGrams: weightGrams, QuotedAt: now, ExpiresAt: now.Add(domain.QuoteTTL),
	}, nil
}

// CreateAuto opens the shipment Order asks for after a verified capture
// and committed stock. It plans the package; no carrier is called.
func (uc *ShipmentUseCase) CreateAuto(ctx context.Context, in CreateShipmentInput) (*domain.Shipment, error) {
	return uc.resolveAndCreate(ctx, in)
}

// resolveAndCreate is idempotent per vendor order: a retry returns the
// existing shipment and never re-prices it.
func (uc *ShipmentUseCase) resolveAndCreate(ctx context.Context, in CreateShipmentInput) (*domain.Shipment, error) {
	if existing, err := uc.Shipments.FindByVendorOrderID(ctx, in.VendorOrderID); err == nil {
		return existing, nil
	} else if !errors.Is(err, repository.ErrShipmentNotFound) {
		return nil, apperror.Internal(err)
	}
	var quote *domain.Quote
	if in.Quote != nil {
		// What the buyer paid stays, even if the fee rule changed since.
		quote = &domain.Quote{FeeAmount: in.Quote.FeeAmount, CarrierID: in.Quote.CarrierID, ZoneID: in.Quote.ZoneID, FeeRuleID: in.Quote.FeeRuleID}
		if zone, err := uc.Zones.FindByID(ctx, in.Quote.ZoneID); err == nil {
			quote.ZoneName = zone.Name
		}
	} else {
		var err error
		if quote, err = uc.Quote(ctx, in.VendorID, in.Province, in.PackageWeightGrams); err != nil {
			return nil, err
		}
	}
	shipment := &domain.Shipment{
		VendorOrderID: in.VendorOrderID, VendorID: in.VendorID, BuyerID: in.BuyerID, Status: domain.StatusPending,
		CarrierID: &quote.CarrierID, ZoneID: &quote.ZoneID, ZoneName: optionalString(quote.ZoneName), FeeRuleID: &quote.FeeRuleID,
		FeeAmount: quote.FeeAmount, PackageWeightGrams: &in.PackageWeightGrams,
		RecipientName: &in.RecipientName, Phone: &in.Phone, Province: &in.Province,
		District: &in.District, Ward: &in.Ward, StreetAddress: &in.StreetAddress,
	}
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		if err := uc.Shipments.Create(ctx, shipment); err != nil {
			return err
		}
		_, err := uc.Events.Insert(ctx, &domain.TrackingEvent{ShipmentID: shipment.ID, Status: domain.StatusPending, ActorRole: domain.ActorSystem})
		return err
	})
	if errors.Is(err, repository.ErrShipmentAlreadyExists) {
		return uc.Shipments.FindByVendorOrderID(ctx, in.VendorOrderID)
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return shipment, nil
}

// CreateOrGet is the vendor's fallback when Order's automatic creation has
// not happened yet. Order's snapshot decides the owner, destination, fee and
// whether the package may ship at all.
func (uc *ShipmentUseCase) CreateOrGet(ctx context.Context, userID, vendorOrderID string) (*domain.Shipment, error) {
	vo, err := uc.Orders.GetVendorOrder(ctx, vendorOrderID)
	if err != nil {
		return nil, err
	}
	vendorID, err := uc.Vendors.GetApprovedVendorID(ctx, userID, vo.VendorID)
	if err != nil {
		return nil, apperror.Forbidden("You do not have access to this order")
	}
	if notShippableStatuses[vo.Status] || !vo.Fulfillable {
		return nil, apperror.Conflict("This order is not ready to be shipped")
	}
	return uc.resolveAndCreate(ctx, CreateShipmentInput{
		VendorOrderID: vendorOrderID, VendorID: vendorID, BuyerID: vo.BuyerID, PackageWeightGrams: vo.PackageWeightGrams,
		RecipientName: vo.RecipientName, Phone: vo.Phone, Province: vo.Province,
		District: vo.District, Ward: vo.Ward, StreetAddress: vo.StreetAddress, Quote: vo.Quote,
	})
}

// authorize checks the actor may act on the shipment: the vendor owning
// it, or an admin re-verified with Identity.
func (uc *ShipmentUseCase) authorize(ctx context.Context, actor Actor, s *domain.Shipment) error {
	switch actor.Role {
	case domain.ActorVendor:
		if _, err := uc.Vendors.GetApprovedVendorID(ctx, actor.ID, s.VendorID); err != nil {
			return apperror.Forbidden("You do not have access to this shipment")
		}
		return nil
	case domain.ActorAdmin:
		if uc.Identity == nil {
			return apperror.Internal(errors.New("role verification is not configured"))
		}
		return uc.Identity.RequireRole(ctx, actor.ID, "admin")
	}
	return apperror.Forbidden("You do not have access to this shipment")
}

// auditAdmin records an admin's action on a shipment in the caller's
// transaction; a vendor's own actions stay on the shipment timeline only.
func (uc *ShipmentUseCase) auditAdmin(ctx context.Context, actor Actor, action, entityType, entityID string, reason *string, changes map[string]any) error {
	if actor.Role != domain.ActorAdmin {
		return nil
	}
	if uc.Audit == nil {
		return apperror.Internal(errors.New("admin audit is not configured"))
	}
	return uc.Audit.Record(ctx, domain.AdminAction{ActorID: actor.ID, Action: action, EntityType: entityType, EntityID: entityID, Reason: reason, Changes: changes})
}

func (uc *ShipmentUseCase) load(ctx context.Context, id string) (*domain.Shipment, error) {
	s, err := uc.Shipments.FindByID(ctx, id)
	if errors.Is(err, repository.ErrShipmentNotFound) {
		return nil, apperror.NotFound("Shipment not found")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return s, nil
}

// act runs one action on a locked shipment in a transaction, after the
// actor is authorized. A concurrent change surfaces as a conflict.
func (uc *ShipmentUseCase) act(ctx context.Context, actor Actor, id string, fn func(ctx context.Context, s *domain.Shipment) error) (*domain.Shipment, error) {
	s, err := uc.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := uc.authorize(ctx, actor, s); err != nil {
		return nil, err
	}
	var out *domain.Shipment
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		locked, err := uc.Shipments.LockByID(ctx, id)
		if err != nil {
			return err
		}
		if err := fn(ctx, locked); err != nil {
			return err
		}
		out = locked
		return nil
	})
	if err != nil {
		return nil, mapError(err)
	}
	uc.wake()
	return out, nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	var app *apperror.Error
	switch {
	case errors.As(err, &app):
		return app
	case errors.Is(err, repository.ErrStaleState):
		return apperror.Conflict("This shipment changed meanwhile; reload and try again")
	case errors.Is(err, repository.ErrShipmentNotFound):
		return apperror.NotFound("Shipment not found")
	}
	return apperror.Internal(err)
}

func (uc *ShipmentUseCase) wake() {
	if uc.Wake != nil {
		uc.Wake()
	}
}

// transition applies one status change with its timeline entry and, for
// shipped/delivered/returned, the Order outbox row, all in the caller's
// transaction.
func (uc *ShipmentUseCase) transition(ctx context.Context, s *domain.Shipment, to domain.Status, c repository.Change, actor Actor, note *string, key *string) error {
	if !domain.CanTransition(s.Status, to) {
		return apperror.Conflict("Cannot move this shipment from " + string(s.Status) + " to " + string(to))
	}
	from := s.Status
	if err := uc.Shipments.Transition(ctx, s, to, c); err != nil {
		return err
	}
	e := &domain.TrackingEvent{ShipmentID: s.ID, Status: to, Note: note, ActorRole: actor.Role, EventKey: key}
	if actor.ID != "" {
		e.ActorID = &actor.ID
	}
	if _, err := uc.Events.Insert(ctx, e); err != nil {
		return err
	}
	if err := uc.auditAdmin(ctx, actor, "shipment_"+string(to), domain.AuditShipment, s.ID, note,
		map[string]any{"status": domain.Change(from, to)}); err != nil {
		return err
	}
	if event, ok := domain.OrderEventFor(to); ok {
		if err := uc.Outbox.Enqueue(ctx, repository.OutboxEvent{ShipmentID: s.ID, VendorOrderID: s.VendorOrderID, Type: event,
			OccurredAt: uc.Now().UTC(), TrackingNumber: s.TrackingNumber}); err != nil {
			return err
		}
	}
	uc.Log.Info().Str("shipment_id", s.ID).Str("vendor_order_id", s.VendorOrderID).Str("status", string(to)).
		Str("actor_role", string(actor.Role)).Msg("shipment_status_changed")
	return nil
}

// MarkReady records that the vendor packed the order.
func (uc *ShipmentUseCase) MarkReady(ctx context.Context, actor Actor, id string) (*domain.Shipment, error) {
	return uc.act(ctx, actor, id, func(ctx context.Context, s *domain.Shipment) error {
		if s.Status == domain.StatusReadyToShip {
			return nil
		}
		return uc.transition(ctx, s, domain.StatusReadyToShip, repository.Change{}, actor, nil, nil)
	})
}

// MarkShipped hands the package to the carrier with its tracking number
// (manual tracking, audited). Order must still let the vendor order ship:
// a cancelled or refunded order, or one without a verified capture and
// committed stock, cannot be shipped.
func (uc *ShipmentUseCase) MarkShipped(ctx context.Context, actor Actor, id, trackingNumber string) (*domain.Shipment, error) {
	tracking, err := domain.NormalizeTrackingNumber(trackingNumber)
	if err != nil {
		return nil, err
	}
	s, err := uc.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.Status == domain.StatusPending || s.Status == domain.StatusReadyToShip {
		vo, err := uc.Orders.GetVendorOrder(ctx, s.VendorOrderID)
		if err != nil {
			return nil, err
		}
		if notShippableStatuses[vo.Status] || !vo.Fulfillable {
			return nil, apperror.Conflict("Order does not allow this package to ship (not paid, stock not committed, or cancelled)")
		}
	}
	return uc.act(ctx, actor, id, func(ctx context.Context, s *domain.Shipment) error {
		if s.Status == domain.StatusShipped && s.TrackingNumber != nil && *s.TrackingNumber == tracking {
			return nil // a retried request
		}
		if s.Status == domain.StatusPending {
			if err := uc.transition(ctx, s, domain.StatusReadyToShip, repository.Change{}, actor, nil, nil); err != nil {
				return err
			}
		}
		now := uc.Now().UTC()
		s.TrackingNumber = &tracking
		note := "Tracking number " + tracking
		return uc.transition(ctx, s, domain.StatusShipped, repository.Change{TrackingNumber: &tracking, ShippedAt: &now}, actor, &note, nil)
	})
}

// UpdateTracking corrects the tracking number of a package in transit.
func (uc *ShipmentUseCase) UpdateTracking(ctx context.Context, actor Actor, id, trackingNumber, reason string) (*domain.Shipment, error) {
	tracking, err := domain.NormalizeTrackingNumber(trackingNumber)
	if err != nil {
		return nil, err
	}
	why, err := domain.ValidateNote(reason, true, "Reason")
	if err != nil {
		return nil, err
	}
	return uc.act(ctx, actor, id, func(ctx context.Context, s *domain.Shipment) error {
		if s.Status != domain.StatusShipped {
			return apperror.Conflict("Only a package in transit can get a new tracking number")
		}
		if err := uc.Shipments.UpdateTracking(ctx, s, tracking); err != nil {
			return err
		}
		previous := s.TrackingNumber
		s.TrackingNumber = &tracking
		note := "Tracking number changed to " + tracking + ": " + *why
		if _, err := uc.Events.Insert(ctx, &domain.TrackingEvent{ShipmentID: s.ID, Status: s.Status, Note: &note, ActorID: &actor.ID, ActorRole: actor.Role}); err != nil {
			return err
		}
		return uc.auditAdmin(ctx, actor, "tracking_number_changed", domain.AuditShipment, s.ID, why,
			map[string]any{"tracking_number": domain.Change(previous, tracking)})
	})
}

// RecordFailedAttempt notes a failed delivery attempt; the package stays
// in transit until it is delivered or returned.
func (uc *ShipmentUseCase) RecordFailedAttempt(ctx context.Context, actor Actor, id, reason string) (*domain.Shipment, error) {
	why, err := domain.ValidateNote(reason, true, "Reason")
	if err != nil {
		return nil, err
	}
	return uc.act(ctx, actor, id, func(ctx context.Context, s *domain.Shipment) error {
		if s.Status != domain.StatusShipped {
			return apperror.Conflict("Only a package in transit can have a delivery attempt")
		}
		if err := uc.Shipments.RecordFailedAttempt(ctx, s, *why); err != nil {
			return err
		}
		s.LastAttemptReason = why
		note := "Delivery attempt failed: " + *why
		if _, err := uc.Events.Insert(ctx, &domain.TrackingEvent{ShipmentID: s.ID, Status: s.Status, Note: &note, ActorID: &actor.ID, ActorRole: actor.Role}); err != nil {
			return err
		}
		return uc.auditAdmin(ctx, actor, "delivery_attempt_failed", domain.AuditShipment, s.ID, why,
			map[string]any{"failed_attempts": s.FailedAttempts})
	})
}

// MarkDelivered records the carrier's delivery confirmation. Order then
// decides the vendor order is completed.
func (uc *ShipmentUseCase) MarkDelivered(ctx context.Context, actor Actor, id, note string) (*domain.Shipment, error) {
	n, err := domain.ValidateNote(note, false, "Note")
	if err != nil {
		return nil, err
	}
	return uc.act(ctx, actor, id, func(ctx context.Context, s *domain.Shipment) error {
		if s.Status == domain.StatusDelivered {
			return nil
		}
		now := uc.Now().UTC()
		return uc.transition(ctx, s, domain.StatusDelivered, repository.Change{DeliveredAt: &now}, actor, n, nil)
	})
}

// MarkReturned records that the package came back to the vendor. It is not
// treated as restocked or refunded; Order and an operator decide that.
func (uc *ShipmentUseCase) MarkReturned(ctx context.Context, actor Actor, id, reason string) (*domain.Shipment, error) {
	why, err := domain.ValidateNote(reason, true, "Reason")
	if err != nil {
		return nil, err
	}
	return uc.act(ctx, actor, id, func(ctx context.Context, s *domain.Shipment) error {
		if s.Status == domain.StatusReturned {
			return nil
		}
		now := uc.Now().UTC()
		return uc.transition(ctx, s, domain.StatusReturned, repository.Change{ReturnedAt: &now}, actor, why, nil)
	})
}

// Advance keeps the previous single-step endpoint working by mapping it to
// the audited actions. Cancelling is Order's decision, not the vendor's.
func (uc *ShipmentUseCase) Advance(ctx context.Context, userID, shipmentID string, newStatus domain.Status, trackingNumber string) (*domain.Shipment, error) {
	actor := Actor{ID: userID, Role: domain.ActorVendor}
	switch newStatus {
	case domain.StatusReadyToShip:
		return uc.MarkReady(ctx, actor, shipmentID)
	case domain.StatusShipped:
		return uc.MarkShipped(ctx, actor, shipmentID, trackingNumber)
	case domain.StatusDelivered:
		return uc.MarkDelivered(ctx, actor, shipmentID, "")
	}
	return nil, apperror.Conflict("A shipment is cancelled by cancelling its order")
}

// CancelForVendorOrder is Order cancelling (or refunding) a vendor order.
// Before handover the shipment is cancelled; after handover the carrier is
// asked to intercept and the shipment waits for its decision, never
// assuming the package was stopped.
func (uc *ShipmentUseCase) CancelForVendorOrder(ctx context.Context, vendorOrderID string) error {
	shipment, err := uc.Shipments.FindByVendorOrderID(ctx, vendorOrderID)
	if errors.Is(err, repository.ErrShipmentNotFound) {
		return nil
	}
	if err != nil {
		return apperror.Internal(err)
	}
	system := Actor{Role: domain.ActorSystem}
	switch {
	case domain.IsCancellable(shipment.Status):
		err := uc.Tx.Run(ctx, func(ctx context.Context) error {
			s, err := uc.Shipments.LockByID(ctx, shipment.ID)
			if err != nil || !domain.IsCancellable(s.Status) {
				return err
			}
			now := uc.Now().UTC()
			note := "Order cancelled"
			return uc.transition(ctx, s, domain.StatusCancelled, repository.Change{CancelledAt: &now}, system, &note, nil)
		})
		return mapError(err)
	case shipment.Status == domain.StatusShipped:
		return uc.requestInterception(ctx, shipment)
	}
	return nil
}

func (uc *ShipmentUseCase) requestInterception(ctx context.Context, shipment *domain.Shipment) error {
	result, err := uc.Carrier.RequestInterception(ctx, carrier.RequestInterceptionInput{
		ShipmentID: shipment.ID, TrackingNumber: derefString(shipment.TrackingNumber), CarrierID: derefString(shipment.CarrierID),
	})
	if err != nil {
		return apperror.Internal(err)
	}
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		s, err := uc.Shipments.LockByID(ctx, shipment.ID)
		if err != nil {
			return err
		}
		if s.Status != domain.StatusShipped {
			return nil // delivered or already intercepted meanwhile
		}
		now := uc.Now().UTC()
		note := "Order cancelled: interception requested from the carrier, awaiting its decision"
		return uc.transition(ctx, s, domain.StatusInterceptionRequested,
			repository.Change{InterceptRef: &result.ProviderReferenceID, InterceptAt: &now}, Actor{Role: domain.ActorSystem}, &note, nil)
	})
	if err != nil {
		return mapError(err)
	}
	uc.Log.Warn().Str("shipment_id", shipment.ID).Msg("shipment_interception_requested")
	return nil
}

// ResolveInterception records the carrier's answer, by an operator after
// calling the carrier (manual mode) or from the carrier's webhook.
// Accepted means the package is on its way back (cancelled), not that it
// is back in stock; rejected means delivery continues.
func (uc *ShipmentUseCase) ResolveInterception(ctx context.Context, actor Actor, id string, accepted bool, note string) (*domain.Shipment, error) {
	n, err := domain.ValidateNote(note, true, "Note")
	if err != nil {
		return nil, err
	}
	return uc.act(ctx, actor, id, func(ctx context.Context, s *domain.Shipment) error {
		return uc.applyInterception(ctx, s, accepted, *n, actor, nil)
	})
}

func (uc *ShipmentUseCase) applyInterception(ctx context.Context, s *domain.Shipment, accepted bool, reason string, actor Actor, key *string) error {
	if s.Status != domain.StatusInterceptionRequested {
		return apperror.Conflict("This shipment has no interception awaiting a decision")
	}
	now := uc.Now().UTC()
	to, note := domain.StatusShipped, "Carrier could not intercept the package; delivery continues"
	change := repository.Change{InterceptResolved: &now}
	if accepted {
		to, note = domain.StatusCancelled, "Carrier intercepted the package; it is on its way back to the vendor"
		change.CancelledAt = &now
	}
	if reason != "" {
		note += " (" + reason + ")"
	}
	return uc.transition(ctx, s, to, change, actor, &note, key)
}

// SimulateCarrierDecision stands in for the carrier's callback with the
// mock carrier only (forbidden in production by configuration).
func (uc *ShipmentUseCase) SimulateCarrierDecision(ctx context.Context, userID, shipmentID string, accepted bool, reason string) (*domain.Shipment, error) {
	if uc.Simulator == nil {
		return nil, apperror.Validation("Simulating a carrier decision is only available with the mock carrier provider")
	}
	s, err := uc.load(ctx, shipmentID)
	if err != nil {
		return nil, err
	}
	if err := uc.authorize(ctx, Actor{ID: userID, Role: domain.ActorVendor}, s); err != nil {
		return nil, err
	}
	if s.Status != domain.StatusInterceptionRequested {
		return nil, apperror.Conflict("This shipment has no interception request awaiting a decision")
	}
	payload, signature, err := uc.Simulator.BuildSignedEvent(derefString(s.InterceptProviderRef), accepted, reason)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if err := uc.ProcessCarrierWebhook(ctx, payload, signature); err != nil {
		return nil, err
	}
	return uc.load(ctx, shipmentID)
}

// ProcessCarrierWebhook verifies a carrier delivery and applies it once:
// the event key and the compare-and-set status change commit together, so
// a repeated or late delivery changes nothing.
func (uc *ShipmentUseCase) ProcessCarrierWebhook(ctx context.Context, payload []byte, signatureHeader string) error {
	event, err := uc.Verifier.Verify(payload, signatureHeader)
	if err != nil {
		uc.Log.Warn().Msg("shipment_carrier_webhook_rejected")
		return apperror.Unauthorized("Invalid webhook signature")
	}
	shipment, err := uc.Shipments.FindByInterceptProviderRef(ctx, event.ProviderReferenceID)
	if errors.Is(err, repository.ErrShipmentNotFound) {
		uc.Log.Warn().Str("provider_reference_id", event.ProviderReferenceID).Msg("shipment_carrier_webhook_unknown_reference")
		return nil
	}
	if err != nil {
		return apperror.Internal(err)
	}
	key := "carrier:" + event.EventID
	if event.EventID == "" {
		key = "carrier:interception:" + event.ProviderReferenceID
	}
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		s, err := uc.Shipments.LockByID(ctx, shipment.ID)
		if err != nil {
			return err
		}
		if seen, err := uc.Events.Exists(ctx, s.ID, key); err != nil || seen {
			return err
		}
		if s.Status != domain.StatusInterceptionRequested {
			// Late or repeated decision: record it, change nothing.
			note := "Carrier decision received after the shipment moved on; ignored"
			_, err := uc.Events.Insert(ctx, &domain.TrackingEvent{ShipmentID: s.ID, Status: s.Status, Note: &note, ActorRole: domain.ActorCarrier, EventKey: &key})
			return err
		}
		return uc.applyInterception(ctx, s, event.Accepted, event.Reason, Actor{Role: domain.ActorCarrier}, &key)
	})
	if err != nil {
		return mapError(err)
	}
	uc.wake()
	return nil
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ListMine lists a vendor's shipments; final ones without the buyer's
// contact details.
func (uc *ShipmentUseCase) ListMine(ctx context.Context, userID, vendorID string, limit, offset int) ([]*domain.Shipment, error) {
	vendorID, err := uc.Vendors.GetApprovedVendorID(ctx, userID, vendorID)
	if err != nil {
		return nil, err
	}
	shipments, err := uc.Shipments.ListByVendor(ctx, vendorID, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	for i, s := range shipments {
		shipments[i] = s.ForVendor()
	}
	return shipments, nil
}

// ListForBuyer lists the buyer's own shipments.
func (uc *ShipmentUseCase) ListForBuyer(ctx context.Context, buyerID string, limit, offset int) ([]*domain.Shipment, error) {
	shipments, err := uc.Shipments.ListByBuyer(ctx, buyerID, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return shipments, nil
}

// GetByVendorOrderID returns a vendor's shipment for one of its orders.
func (uc *ShipmentUseCase) GetByVendorOrderID(ctx context.Context, userID, vendorOrderID string) (*domain.Shipment, error) {
	shipment, err := uc.Shipments.FindByVendorOrderID(ctx, vendorOrderID)
	if errors.Is(err, repository.ErrShipmentNotFound) {
		return nil, apperror.NotFound("Shipment not found")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if _, err := uc.Vendors.GetApprovedVendorID(ctx, userID, shipment.VendorID); err != nil {
		return nil, apperror.Forbidden("You do not have access to this shipment")
	}
	return shipment.ForVendor(), nil
}

// ListEventsForShipment is the timeline, for the shipment's buyer or vendor.
func (uc *ShipmentUseCase) ListEventsForShipment(ctx context.Context, userID, shipmentID string) ([]*domain.TrackingEvent, error) {
	shipment, err := uc.load(ctx, shipmentID)
	if err != nil {
		return nil, err
	}
	if shipment.BuyerID != userID {
		if _, err := uc.Vendors.GetApprovedVendorID(ctx, userID, shipment.VendorID); err != nil {
			return nil, apperror.Forbidden("You do not have access to this shipment")
		}
	}
	events, err := uc.Events.ListForShipment(ctx, shipmentID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return events, nil
}

// ----- admin operations -----

// Operations is the admin view of fulfillment problems.
type Operations struct {
	Counts map[string]int64              `json:"counts"`
	Outbox []repository.OutboxProblem    `json:"outbox"`
	Lists  map[string][]*domain.Shipment `json:"-"`
}

var attentionKinds = []string{"fulfillment_lag", "tracking_stale", "interception_pending", "failed_attempts"}

func (uc *ShipmentUseCase) requireAdmin(ctx context.Context, adminID string) error {
	return uc.authorize(ctx, Actor{ID: adminID, Role: domain.ActorAdmin}, nil)
}

// Report is the counters the worker logs and admins see.
func (uc *ShipmentUseCase) Report(ctx context.Context) (map[string]int64, error) {
	counts, err := uc.Shipments.AttentionCounts(ctx)
	if err != nil {
		return nil, err
	}
	pending, review, err := uc.Outbox.Counts(ctx)
	if err != nil {
		return nil, err
	}
	counts["order_events_pending"], counts["order_events_review"] = pending, review
	return counts, nil
}

func (uc *ShipmentUseCase) AdminOperations(ctx context.Context, adminID string) (*Operations, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	out := &Operations{Lists: map[string][]*domain.Shipment{}}
	var err error
	if out.Counts, err = uc.Report(ctx); err != nil {
		return nil, apperror.Internal(err)
	}
	if out.Outbox, err = uc.Outbox.Problems(ctx, 50); err != nil {
		return nil, apperror.Internal(err)
	}
	for _, kind := range attentionKinds {
		if out.Lists[kind], err = uc.Shipments.ListAttention(ctx, kind, 50, 0); err != nil {
			return nil, apperror.Internal(err)
		}
	}
	return out, nil
}

// AdminGet returns a shipment with its full detail.
func (uc *ShipmentUseCase) AdminGet(ctx context.Context, adminID, id string) (*domain.Shipment, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	return uc.load(ctx, id)
}

// RetryOrderEvent sends a parked outbox event to Order again; the reason is
// kept on the shipment's timeline.
func (uc *ShipmentUseCase) RetryOrderEvent(ctx context.Context, adminID, outboxID, shipmentID, reason string) error {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return err
	}
	why, err := domain.ValidateNote(reason, true, "Reason")
	if err != nil {
		return err
	}
	s, err := uc.load(ctx, shipmentID)
	if err != nil {
		return err
	}
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		if err := uc.Outbox.Requeue(ctx, outboxID); err != nil {
			return err
		}
		note := "Order notification resent by admin: " + *why
		if _, err := uc.Events.Insert(ctx, &domain.TrackingEvent{ShipmentID: s.ID, Status: s.Status, Note: &note, ActorID: &adminID, ActorRole: domain.ActorAdmin}); err != nil {
			return err
		}
		return uc.auditAdmin(ctx, Actor{ID: adminID, Role: domain.ActorAdmin}, "order_event_resent", domain.AuditOrderEvent, outboxID, why,
			map[string]any{"shipment_id": s.ID})
	})
	if err != nil {
		return mapError(err)
	}
	uc.wake()
	return nil
}

// RedactAddresses removes buyer contact details from final shipments past
// the retention period.
func (uc *ShipmentUseCase) RedactAddresses(ctx context.Context, retention time.Duration) (int64, error) {
	return uc.Shipments.RedactAddresses(ctx, retention, 500)
}
