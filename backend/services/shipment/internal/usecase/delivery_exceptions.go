package usecase

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
)

// AF-04: Shipment records failed deliveries and runs the redelivery
// attempts Order decides; Order owns the resolution, the money and the
// stock.

// enqueueException tells Order a delivery failed for good, in the caller's
// transaction (one fact per shipment and type). Off with the flag: the
// status facts alone go out, as before.
func (uc *ShipmentUseCase) enqueueException(ctx context.Context, s *domain.Shipment, kind domain.ExceptionType, note *string) error {
	if !uc.DeliveryResolution {
		return nil
	}
	attemptNo, failed := s.AttemptNo, s.FailedAttempts
	if attemptNo == 0 {
		attemptNo = 1
	}
	if err := uc.Outbox.Enqueue(ctx, repository.OutboxEvent{ShipmentID: s.ID, VendorOrderID: s.VendorOrderID, Type: kind.OutboxType(),
		OccurredAt: uc.Now().UTC(), AttemptNo: &attemptNo, FailedAttempts: &failed, Reason: note}); err != nil {
		return err
	}
	uc.Log.Warn().Str("shipment_id", s.ID).Str("vendor_order_id", s.VendorOrderID).Str("exception_type", string(kind)).
		Int("failed_attempts", failed).Int("attempt_no", attemptNo).Msg("shipment_delivery_exception_detected")
	return nil
}

// FailureReport is a shop's or an admin's report that delivery failed for
// good: the package came back (returned) or the carrier lost it (lost,
// admins only, after checking the carrier's evidence).
type FailureReport struct {
	Kind            string
	Reason          string
	ExpectedVersion int64
}

// ReportFailure records the report on the shipment the caller read
// (compare-and-set on its version); Order is told through the outbox.
func (uc *ShipmentUseCase) ReportFailure(ctx context.Context, actor Actor, id string, in FailureReport) (*domain.Shipment, error) {
	if !uc.DeliveryResolution {
		return nil, domain.DeliveryResolutionDisabled()
	}
	to, ok := domain.FailureKinds[in.Kind]
	if !ok {
		return nil, apperror.Validation("kind must be returned or lost")
	}
	if to == domain.StatusLost && actor.Role != domain.ActorAdmin {
		return nil, apperror.Forbidden("Only the marketplace can declare a package lost, after checking with the carrier")
	}
	why, err := domain.ValidateNote(in.Reason, true, "Reason")
	if err != nil {
		return nil, err
	}
	return uc.act(ctx, actor, id, func(ctx context.Context, s *domain.Shipment) error {
		if s.Status == to {
			return nil // a retried report
		}
		if s.Version != in.ExpectedVersion {
			return domain.ShipmentChanged()
		}
		now := uc.Now().UTC()
		change := repository.Change{ReturnedAt: &now}
		note := "Package returned to the shop: " + *why
		if to == domain.StatusLost {
			change = repository.Change{LostAt: &now}
			note = "Carrier confirmed the package lost: " + *why
		}
		return uc.transition(ctx, s, to, change, actor, &note, nil)
	})
}

// ReplacementAttempt is Order asking for a redelivery after an attempt
// ended without reaching the buyer. Destination is what the buyer agreed
// to (the order's address or a new one).
type ReplacementAttempt struct {
	OperationID        string
	VendorOrderID      string
	OriginalShipmentID string
	AttemptNo          int
	EligibilityRef     string
	Destination        Destination
}

// Destination is a delivery address snapshot.
type Destination struct {
	RecipientName string
	Phone         string
	Province      string
	District      string
	Ward          string
	StreetAddress string
}

// CreateReplacementAttempt opens attempt n+1 of a vendor order, once per
// operation. The previous attempt must be the latest one and have ended
// returned or lost; it is never reset. No new charge: the redelivery fee
// is the shop's or the marketplace's (AF-04 §4), recorded for reference.
func (uc *ShipmentUseCase) CreateReplacementAttempt(ctx context.Context, in ReplacementAttempt) (*domain.Shipment, error) {
	if !uc.DeliveryResolution {
		return nil, domain.DeliveryResolutionDisabled()
	}
	if existing, err := uc.Shipments.FindByReplacementOperation(ctx, in.OperationID); err == nil {
		if existing.VendorOrderID != in.VendorOrderID || existing.AttemptNo != in.AttemptNo {
			return nil, apperror.Conflict("operation_id was used for another attempt")
		}
		return existing, nil
	} else if !errors.Is(err, repository.ErrShipmentNotFound) {
		return nil, apperror.Internal(err)
	}
	original, err := uc.load(ctx, in.OriginalShipmentID)
	if err != nil {
		return nil, err
	}
	latest, err := uc.Shipments.FindByVendorOrderID(ctx, in.VendorOrderID)
	if err != nil {
		return nil, mapError(err)
	}
	switch {
	case original.VendorOrderID != in.VendorOrderID:
		return nil, apperror.Conflict("The original shipment belongs to another order")
	case latest.Status.Active():
		return nil, domain.ActiveAttemptExists()
	case latest.ID != original.ID || in.AttemptNo != original.AttemptNo+1:
		return nil, apperror.Conflict("Only the latest attempt can be followed by a redelivery (attempt " + strconv.Itoa(latest.AttemptNo+1) + ")")
	case !domain.RedeliverableFrom(original.Status):
		return nil, apperror.Conflict("A redelivery follows a returned or lost package, not a " + string(original.Status) + " one")
	case in.AttemptNo > domain.MaxAttempts:
		return nil, apperror.Conflict("This order reached the maximum number of delivery attempts")
	}
	d := in.Destination
	for _, f := range []string{d.RecipientName, d.Phone, d.Province, d.District, d.StreetAddress} {
		if strings.TrimSpace(f) == "" {
			return nil, apperror.Validation("A complete destination is required for a redelivery")
		}
	}
	weight := int64(0)
	if original.PackageWeightGrams != nil {
		weight = *original.PackageWeightGrams
	}
	shipment := &domain.Shipment{VendorOrderID: original.VendorOrderID, VendorID: original.VendorID, BuyerID: original.BuyerID,
		Status: domain.StatusPending, AttemptNo: in.AttemptNo, OriginalShipmentID: &original.ID, PackageWeightGrams: &weight,
		RecipientName: &d.RecipientName, Phone: &d.Phone, Province: &d.Province, District: &d.District, Ward: &d.Ward, StreetAddress: &d.StreetAddress}
	if original.Province != nil && *original.Province == d.Province && original.CarrierID != nil {
		// Same destination zone: the carrier, zone and fee of attempt 1.
		shipment.CarrierID, shipment.ZoneID, shipment.ZoneName, shipment.FeeRuleID, shipment.FeeAmount =
			original.CarrierID, original.ZoneID, original.ZoneName, original.FeeRuleID, original.FeeAmount
	} else {
		quote, err := uc.quote(ctx, original.VendorID, d.Province, weight, false)
		if err != nil {
			return nil, err
		}
		shipment.CarrierID, shipment.ZoneID, shipment.ZoneName, shipment.FeeRuleID, shipment.FeeAmount =
			&quote.CarrierID, &quote.ZoneID, optionalString(quote.ZoneName), &quote.FeeRuleID, quote.FeeAmount
	}
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		if err := uc.Shipments.CreateReplacement(ctx, shipment, in.OperationID); err != nil {
			return err
		}
		note := "Redelivery attempt " + strconv.Itoa(in.AttemptNo) + " after attempt " + strconv.Itoa(original.AttemptNo) +
			" (" + string(original.Status) + "); the shop or marketplace bears the fee"
		_, err := uc.Events.Insert(ctx, &domain.TrackingEvent{ShipmentID: shipment.ID, Status: domain.StatusPending, Note: &note, ActorRole: domain.ActorSystem})
		return err
	})
	switch {
	case errors.Is(err, repository.ErrActiveAttemptExists):
		return nil, domain.ActiveAttemptExists()
	case errors.Is(err, repository.ErrShipmentAlreadyExists):
		if existing, findErr := uc.Shipments.FindByReplacementOperation(ctx, in.OperationID); findErr == nil {
			return existing, nil
		}
		return nil, apperror.Conflict("Attempt " + strconv.Itoa(in.AttemptNo) + " of this order already exists")
	case err != nil:
		return nil, mapError(err)
	}
	uc.Log.Info().Str("shipment_id", shipment.ID).Str("vendor_order_id", shipment.VendorOrderID).Int("attempt_no", in.AttemptNo).
		Str("original_shipment_id", original.ID).Str("eligibility_ref", in.EligibilityRef).Msg("shipment_replacement_attempt_created")
	return shipment, nil
}
