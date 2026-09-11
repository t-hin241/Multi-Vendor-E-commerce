// Package usecase orchestrates Shipment's workflows: a vendor opening a
// shipment for one of their sub-orders (or, now, the system opening one
// automatically right after checkout), advancing it through its own
// fulfillment sub-states (pending -> ready_to_ship -> shipped ->
// delivered, or cancelled while nothing has shipped yet), and quoting its
// fee from the vendor's default carrier and the buyer's destination zone.
// Shipment tracks logistics detail for the vendor's and admin's own
// operational visibility; it never writes back to Order's order/vendor-
// order status, which Order alone owns.
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

type ShipmentUseCase struct {
	shipments        ShipmentRepositoryPort
	vendorMethods    VendorShippingMethodRepositoryPort
	zones            ZoneRepositoryPort
	feeRules         FeeRuleRepositoryPort
	events           TrackingEventRepositoryPort
	vendors          VendorGateway
	orders           OrderGateway
	carrierProvider  carrier.Provider
	carrierVerifier  carrier.Verifier
	carrierSimulator CarrierSimulator
	log              zerolog.Logger
}

func NewShipmentUseCase(
	shipments ShipmentRepositoryPort,
	vendorMethods VendorShippingMethodRepositoryPort,
	zones ZoneRepositoryPort,
	feeRules FeeRuleRepositoryPort,
	events TrackingEventRepositoryPort,
	vendors VendorGateway,
	orders OrderGateway,
	carrierProvider carrier.Provider,
	carrierVerifier carrier.Verifier,
	carrierSimulator CarrierSimulator,
	log zerolog.Logger,
) *ShipmentUseCase {
	return &ShipmentUseCase{
		shipments: shipments, vendorMethods: vendorMethods, zones: zones, feeRules: feeRules,
		events: events, vendors: vendors, orders: orders,
		carrierProvider: carrierProvider, carrierVerifier: carrierVerifier, carrierSimulator: carrierSimulator,
		log: log,
	}
}

// notShippableStatuses are vendor-order statuses that can never get a
// shipment: the sub-order is already done or voided.
var notShippableStatuses = map[string]bool{
	"cancelled": true,
	"refunded":  true,
}

// CreateShipmentInput carries everything Order already knows about a
// freshly-checked-out vendor sub-order — Shipment trusts this input as
// coming from Order over the internal, network-only boundary, exactly like
// every other /internal/... call in this codebase.
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
}

// CreateAuto is the automatic entry point Order calls right after
// checkout, with everything already known and passed in directly. See
// resolveAndCreate for the actual resolution/persistence logic, shared
// with the vendor-triggered fallback below.
func (uc *ShipmentUseCase) CreateAuto(ctx context.Context, in CreateShipmentInput) (*domain.Shipment, error) {
	return uc.resolveAndCreate(ctx, in)
}

// resolveAndCreate resolves the vendor's default shipping method, matches
// the destination province to a zone, quotes the current fee rule for
// that (carrier, zone) pair, and persists the shipment with the computed
// fee already snapshotted — idempotent, so a retried call never produces
// a duplicate shipment or a different fee.
func (uc *ShipmentUseCase) resolveAndCreate(ctx context.Context, in CreateShipmentInput) (*domain.Shipment, error) {
	if existing, err := uc.shipments.FindByVendorOrderID(ctx, in.VendorOrderID); err == nil {
		return existing, nil
	} else if !errors.Is(err, repository.ErrShipmentNotFound) {
		return nil, apperror.Internal(err)
	}

	method, err := uc.vendorMethods.FindDefaultForVendor(ctx, in.VendorID)
	if err != nil {
		if errors.Is(err, repository.ErrVendorShippingMethodNotFound) {
			return nil, apperror.Validation("This shop has not set up a shipping method yet")
		}
		return nil, apperror.Internal(err)
	}

	zone, err := uc.zones.FindZoneByProvinceCode(ctx, in.Province)
	if err != nil {
		if errors.Is(err, repository.ErrZoneNotFound) {
			return nil, apperror.Validation("Shipping is not available for this destination yet")
		}
		return nil, apperror.Internal(err)
	}

	rule, err := uc.feeRules.FindCurrent(ctx, method.CarrierID, zone.ID)
	if err != nil {
		if errors.Is(err, repository.ErrFeeRuleNotFound) {
			return nil, apperror.Validation("Shipping fee is not configured for this destination yet")
		}
		return nil, apperror.Internal(err)
	}

	feeAmount := domain.ComputeShippingFee(*rule, in.PackageWeightGrams)

	shipment := &domain.Shipment{
		VendorOrderID: in.VendorOrderID, VendorID: in.VendorID, BuyerID: in.BuyerID, Status: domain.StatusPending,
		CarrierID: &method.CarrierID, ZoneID: &zone.ID, ZoneName: &zone.Name, FeeRuleID: &rule.ID, FeeAmount: feeAmount,
		PackageWeightGrams: &in.PackageWeightGrams,
		RecipientName:      &in.RecipientName, Phone: &in.Phone, Province: &in.Province,
		District: &in.District, Ward: &in.Ward, StreetAddress: &in.StreetAddress,
	}
	if err := uc.shipments.Create(ctx, shipment); err != nil {
		if errors.Is(err, repository.ErrShipmentAlreadyExists) {
			return uc.shipments.FindByVendorOrderID(ctx, in.VendorOrderID)
		}
		return nil, apperror.Internal(err)
	}
	if err := uc.recordEvent(ctx, shipment.ID, domain.StatusPending, nil); err != nil {
		return nil, err
	}
	return shipment, nil
}

// CancelForVendorOrder voids a shipment when its vendor order is cancelled
// or refunded. It's a no-op — not an error — when there's no shipment yet
// (creation is best-effort and may have failed) or the shipment has already
// moved past the point where cancelling still makes sense (delivered,
// cancelled, or an interception is already in flight). A shipment that has
// already been handed to the carrier ("shipped") can't be cancelled
// directly — instead this asks the carrier to intercept it; the outcome
// arrives later via ProcessCarrierWebhook. Order calls this unconditionally
// and best-effort for every cancelled vendor order, so both branches return
// nil rather than surfacing an error the caller would just log anyway.
func (uc *ShipmentUseCase) CancelForVendorOrder(ctx context.Context, vendorOrderID string) error {
	shipment, err := uc.shipments.FindByVendorOrderID(ctx, vendorOrderID)
	if err != nil {
		if errors.Is(err, repository.ErrShipmentNotFound) {
			return nil
		}
		return apperror.Internal(err)
	}

	if domain.IsCancellable(shipment.Status) {
		if err := uc.shipments.Advance(ctx, shipment.ID, domain.StatusCancelled, nil, nil, nil); err != nil {
			return apperror.Internal(err)
		}
		return uc.recordEvent(ctx, shipment.ID, domain.StatusCancelled, nil)
	}

	if shipment.Status == domain.StatusShipped {
		return uc.requestInterception(ctx, shipment)
	}

	return nil
}

// requestInterception asks the carrier (today: the mock adapter) to pull
// back a shipment already in transit. The decision is never synchronous —
// a real carrier's dispatcher has to be reached first — so this only
// records the request and waits for ProcessCarrierWebhook.
func (uc *ShipmentUseCase) requestInterception(ctx context.Context, shipment *domain.Shipment) error {
	result, err := uc.carrierProvider.RequestInterception(ctx, carrier.RequestInterceptionInput{
		ShipmentID:     shipment.ID,
		TrackingNumber: derefString(shipment.TrackingNumber),
		CarrierID:      derefString(shipment.CarrierID),
	})
	if err != nil {
		return apperror.Internal(err)
	}

	applied, err := uc.shipments.RequestInterception(ctx, shipment.ID, result.ProviderReferenceID)
	if err != nil {
		return apperror.Internal(err)
	}
	if !applied {
		// Lost the race — e.g. the vendor marked it delivered in the
		// meantime. Nothing left to intercept.
		return nil
	}

	note := "Interception requested with the carrier; awaiting their decision"
	return uc.recordEvent(ctx, shipment.ID, domain.StatusInterceptionRequested, &note)
}

// SimulateCarrierDecision lets the vendor stand in for the carrier's own
// callback in local/dev environments — it builds the exact same signed
// event a real carrier's webhook delivery would carry and feeds it through
// ProcessCarrierWebhook, so the two can never drift apart. Only available
// when this deployment is wired with the mock carrier adapter.
func (uc *ShipmentUseCase) SimulateCarrierDecision(ctx context.Context, userID, shipmentID string, accepted bool, reason string) (*domain.Shipment, error) {
	if uc.carrierSimulator == nil {
		return nil, apperror.Validation("Simulating a carrier decision is only available with the mock carrier provider")
	}

	shipment, err := uc.findOwned(ctx, userID, shipmentID)
	if err != nil {
		return nil, err
	}
	if shipment.Status != domain.StatusInterceptionRequested {
		return nil, apperror.Conflict("This shipment has no interception request awaiting a decision")
	}

	payload, signature, err := uc.carrierSimulator.BuildSignedEvent(derefString(shipment.InterceptProviderRef), accepted, reason)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	if err := uc.ProcessCarrierWebhook(ctx, payload, signature); err != nil {
		return nil, err
	}
	return uc.shipments.FindByID(ctx, shipmentID)
}

// ProcessCarrierWebhook is the single entry point any carrier decision
// delivery (or the mock "simulate" path) goes through: verify the
// signature, then apply the outcome idempotently via ResolveInterception's
// conditional UPDATE — a duplicate delivery finds nothing left to resolve
// and is silently ignored, exactly like Payment's duplicate-webhook
// handling.
func (uc *ShipmentUseCase) ProcessCarrierWebhook(ctx context.Context, payload []byte, signatureHeader string) error {
	event, err := uc.carrierVerifier.Verify(payload, signatureHeader)
	if err != nil {
		return apperror.Unauthorized("Invalid webhook signature")
	}

	shipment, err := uc.shipments.FindByInterceptProviderRef(ctx, event.ProviderReferenceID)
	if err != nil {
		if errors.Is(err, repository.ErrShipmentNotFound) {
			uc.log.Warn().Str("provider_reference_id", event.ProviderReferenceID).Msg("carrier webhook received for an unknown interception request")
			return nil
		}
		return apperror.Internal(err)
	}

	resultStatus := domain.StatusShipped
	note := "Carrier could not intercept the package; delivery continues"
	if event.Accepted {
		resultStatus = domain.StatusCancelled
		note = "Carrier intercepted the package; it will be returned to the vendor"
	}
	if event.Reason != "" {
		note += " (" + event.Reason + ")"
	}

	updated, applied, err := uc.shipments.ResolveInterception(ctx, shipment.ID, resultStatus)
	if err != nil {
		return apperror.Internal(err)
	}
	if !applied {
		uc.log.Info().Str("shipment_id", shipment.ID).Msg("duplicate carrier decision ignored")
		return nil
	}

	return uc.recordEvent(ctx, updated.ID, resultStatus, &note)
}

func (uc *ShipmentUseCase) recordEvent(ctx context.Context, shipmentID string, status domain.Status, note *string) error {
	if err := uc.events.Insert(ctx, &domain.TrackingEvent{ShipmentID: shipmentID, Status: status, Note: note}); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// CreateOrGet is the vendor-triggered fallback for when the automatic
// CreateAuto call at checkout failed (e.g. Shipment was briefly
// unreachable) — it re-fetches the vendor order's enriched snapshot from
// Order (which includes the buyer/destination/package-weight details
// Order itself used to quote the fee at checkout time) and runs it
// through the same resolveAndCreate path, so a vendor is never permanently
// stuck with a missing shipment just because one HTTP call once failed.
func (uc *ShipmentUseCase) CreateOrGet(ctx context.Context, userID, vendorOrderID string) (*domain.Shipment, error) {
	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID)
	if err != nil {
		return nil, err
	}

	vo, err := uc.orders.GetVendorOrder(ctx, vendorOrderID)
	if err != nil {
		return nil, err
	}
	if vo.VendorID != vendorID {
		return nil, apperror.Forbidden("You do not have access to this order")
	}
	if notShippableStatuses[vo.Status] {
		return nil, apperror.Conflict("This order is not ready to be shipped")
	}

	return uc.resolveAndCreate(ctx, CreateShipmentInput{
		VendorOrderID: vendorOrderID, VendorID: vendorID, BuyerID: vo.BuyerID, PackageWeightGrams: vo.PackageWeightGrams,
		RecipientName: vo.RecipientName, Phone: vo.Phone, Province: vo.Province,
		District: vo.District, Ward: vo.Ward, StreetAddress: vo.StreetAddress,
	})
}

// Advance moves a shipment forward one step (or voids it). Reaching
// "shipped" requires a tracking number — a buyer can't be told a package
// shipped with nothing to track it by. Carrier is never supplied here: it
// was fixed and its fee already charged at creation time.
func (uc *ShipmentUseCase) Advance(ctx context.Context, userID, shipmentID string, newStatus domain.Status, trackingNumber string) (*domain.Shipment, error) {
	shipment, err := uc.findOwned(ctx, userID, shipmentID)
	if err != nil {
		return nil, err
	}
	if !domain.CanTransition(shipment.Status, newStatus) {
		return nil, apperror.Conflict("Cannot move this shipment from " + string(shipment.Status) + " to " + string(newStatus))
	}

	var trackingPtr *string
	var shippedAt, deliveredAt *time.Time

	if newStatus == domain.StatusShipped {
		if err := domain.ValidateTrackingNumber(trackingNumber); err != nil {
			return nil, err
		}
		trackingPtr = &trackingNumber
		now := time.Now()
		shippedAt = &now
	}
	if newStatus == domain.StatusDelivered {
		now := time.Now()
		deliveredAt = &now
	}
	// ready_to_ship may optionally record a tracking number chosen ahead of
	// the actual handoff, without requiring it yet.
	if newStatus == domain.StatusReadyToShip && trackingNumber != "" {
		trackingPtr = &trackingNumber
	}

	if err := uc.shipments.Advance(ctx, shipment.ID, newStatus, trackingPtr, shippedAt, deliveredAt); err != nil {
		return nil, apperror.Internal(err)
	}
	if err := uc.recordEvent(ctx, shipment.ID, newStatus, nil); err != nil {
		return nil, err
	}

	shipment.Status = newStatus
	if trackingPtr != nil {
		shipment.TrackingNumber = trackingPtr
	}
	if shippedAt != nil {
		shipment.ShippedAt = shippedAt
	}
	if deliveredAt != nil {
		shipment.DeliveredAt = deliveredAt
	}
	return shipment, nil
}

func (uc *ShipmentUseCase) ListMine(ctx context.Context, userID string, limit, offset int) ([]*domain.Shipment, error) {
	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID)
	if err != nil {
		return nil, err
	}

	shipments, err := uc.shipments.ListByVendor(ctx, vendorID, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return shipments, nil
}

// ListForBuyer is the buyer-facing counterpart to ListMine — buyer_id is
// denormalized onto shipments at creation time, so this needs no live
// cross-service ownership check.
func (uc *ShipmentUseCase) ListForBuyer(ctx context.Context, buyerID string, limit, offset int) ([]*domain.Shipment, error) {
	shipments, err := uc.shipments.ListByBuyer(ctx, buyerID, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return shipments, nil
}

func (uc *ShipmentUseCase) GetByVendorOrderID(ctx context.Context, userID, vendorOrderID string) (*domain.Shipment, error) {
	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID)
	if err != nil {
		return nil, err
	}

	shipment, err := uc.shipments.FindByVendorOrderID(ctx, vendorOrderID)
	if err != nil {
		if errors.Is(err, repository.ErrShipmentNotFound) {
			return nil, apperror.NotFound("Shipment not found")
		}
		return nil, apperror.Internal(err)
	}
	if shipment.VendorID != vendorID {
		return nil, apperror.Forbidden("You do not have access to this shipment")
	}
	return shipment, nil
}

// ListEventsForShipment is available to either the vendor or the buyer on
// their own shipment — this is the "tracking" a buyer actually gets to
// see.
func (uc *ShipmentUseCase) ListEventsForShipment(ctx context.Context, userID, shipmentID string) ([]*domain.TrackingEvent, error) {
	shipment, err := uc.shipments.FindByID(ctx, shipmentID)
	if err != nil {
		if errors.Is(err, repository.ErrShipmentNotFound) {
			return nil, apperror.NotFound("Shipment not found")
		}
		return nil, apperror.Internal(err)
	}
	if shipment.BuyerID != userID {
		vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID)
		if err != nil || shipment.VendorID != vendorID {
			return nil, apperror.Forbidden("You do not have access to this shipment")
		}
	}

	events, err := uc.events.ListForShipment(ctx, shipmentID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return events, nil
}

func (uc *ShipmentUseCase) findOwned(ctx context.Context, userID, shipmentID string) (*domain.Shipment, error) {
	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID)
	if err != nil {
		return nil, err
	}

	shipment, err := uc.shipments.FindByID(ctx, shipmentID)
	if err != nil {
		if errors.Is(err, repository.ErrShipmentNotFound) {
			return nil, apperror.NotFound("Shipment not found")
		}
		return nil, apperror.Internal(err)
	}
	if shipment.VendorID != vendorID {
		return nil, apperror.Forbidden("You do not have access to this shipment")
	}
	return shipment, nil
}
