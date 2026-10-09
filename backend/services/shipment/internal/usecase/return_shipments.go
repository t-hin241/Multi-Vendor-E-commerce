package usecase

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
)

// ReturnShipmentPort stores AF-05 return parcels.
type ReturnShipmentPort interface {
	Create(ctx context.Context, s *domain.ReturnShipment) error
	FindByID(ctx context.Context, id string) (*domain.ReturnShipment, error)
	FindByOperation(ctx context.Context, operationID string) (*domain.ReturnShipment, error)
	FindActive(ctx context.Context, returnID string) (*domain.ReturnShipment, error)
	Save(ctx context.Context, s *domain.ReturnShipment) error
	List(ctx context.Context, status string, limit, offset int) ([]*domain.ReturnShipment, error)
	Counts(ctx context.Context) (pending, inTransit, stale int64, err error)
}

// ReturnShipmentUseCase runs the way back (AF-05) for Order: every step
// is Order's command, repeated safely under its operation id.
type ReturnShipmentUseCase struct {
	Tx       Transactor
	Returns  ReturnShipmentPort
	Identity RoleVerifier
	// Enabled is FEATURE_RETURN_SHIPPING_ENABLED: new parcels only; parcels
	// already authorized keep moving after it is turned off.
	Enabled bool
	Log     zerolog.Logger
	Now     func() time.Time
}

func (uc *ReturnShipmentUseCase) now() time.Time {
	if uc.Now != nil {
		return uc.Now().UTC()
	}
	return time.Now().UTC()
}

func returnError(err error) error {
	var app *apperror.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &app):
		return app
	case errors.Is(err, repository.ErrReturnShipmentNotFound):
		return apperror.NotFound("Return shipment not found")
	case errors.Is(err, repository.ErrStaleState):
		return apperror.Conflict("This return shipment changed meanwhile; retry")
	}
	return apperror.Internal(err)
}

// ReturnAuthorization is what Order authorized for a return.
type ReturnAuthorization struct {
	OperationID          string
	ReturnID             string
	OrderID              string
	VendorID             string
	BuyerID              string
	AuthorizationVersion int
	Destination          Destination
	ReceivingHours       string
}

// Authorize opens the return's parcel, or moves a parcel not sent yet to
// a newer authorization (a corrected destination). A sent parcel keeps
// its destination (409 destination_changed). Repeats answer the same.
func (uc *ReturnShipmentUseCase) Authorize(ctx context.Context, in ReturnAuthorization) (*domain.ReturnShipment, error) {
	if existing, err := uc.Returns.FindByOperation(ctx, in.OperationID); err == nil {
		if existing.ReturnID != in.ReturnID {
			return nil, apperror.Conflict("operation_id was used for another return")
		}
		return existing, nil
	} else if !errors.Is(err, repository.ErrReturnShipmentNotFound) {
		return nil, apperror.Internal(err)
	}
	d := in.Destination
	for _, f := range []string{d.RecipientName, d.Phone, d.Province, d.District, d.StreetAddress, in.ReceivingHours} {
		if strings.TrimSpace(f) == "" {
			return nil, apperror.Validation("A complete return destination is required")
		}
	}
	var out *domain.ReturnShipment
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		active, err := uc.Returns.FindActive(ctx, in.ReturnID)
		if err != nil && !errors.Is(err, repository.ErrReturnShipmentNotFound) {
			return err
		}
		if active != nil {
			if active.AuthorizationVersion >= in.AuthorizationVersion {
				out = active // the same or a newer authorization already applies
				return nil
			}
			if active.Status != domain.ReturnPendingDispatch {
				return domain.DestinationChanged()
			}
			active.AuthorizationVersion = in.AuthorizationVersion
			active.RecipientName, active.Phone, active.Province, active.District, active.Ward, active.StreetAddress =
				d.RecipientName, d.Phone, d.Province, d.District, d.Ward, d.StreetAddress
			active.ReceivingHours = in.ReceivingHours
			out = active
			return uc.Returns.Save(ctx, active)
		}
		if !uc.Enabled {
			return domain.ReturnShippingDisabled()
		}
		out = &domain.ReturnShipment{ReturnID: in.ReturnID, OrderID: in.OrderID, VendorID: in.VendorID, BuyerID: in.BuyerID,
			AuthorizationVersion: in.AuthorizationVersion, OperationID: in.OperationID, Status: domain.ReturnPendingDispatch,
			RecipientName: d.RecipientName, Phone: d.Phone, Province: d.Province, District: d.District, Ward: d.Ward,
			StreetAddress: d.StreetAddress, ReceivingHours: in.ReceivingHours}
		return uc.Returns.Create(ctx, out)
	})
	if errors.Is(err, repository.ErrActiveReturnShipment) || errors.Is(err, repository.ErrShipmentAlreadyExists) {
		return uc.Returns.FindActive(ctx, in.ReturnID)
	}
	if err != nil {
		return nil, returnError(err)
	}
	uc.Log.Info().Str("return_shipment_id", out.ID).Str("return_id", out.ReturnID).Int("authorization_version", out.AuthorizationVersion).
		Msg("shipment_return_authorized")
	return out, nil
}

// ReturnDispatch is the buyer's dispatch Order forwards.
type ReturnDispatch struct {
	OperationID    string
	CarrierName    string
	TrackingNumber string
	DispatchedAt   time.Time
}

// Dispatch records the buyer's carrier and tracking; the parcel is in
// transit. A repeat of the same operation answers the same.
func (uc *ReturnShipmentUseCase) Dispatch(ctx context.Context, id string, in ReturnDispatch) (*domain.ReturnShipment, error) {
	carrier, err := domain.ValidateCarrierName(in.CarrierName)
	if err != nil {
		return nil, err
	}
	tracking, err := domain.NormalizeTrackingNumber(in.TrackingNumber)
	if err != nil {
		return nil, err
	}
	return uc.step(ctx, id, func(s *domain.ReturnShipment) (bool, error) {
		if s.DispatchOperationID != nil && *s.DispatchOperationID == in.OperationID {
			return false, nil
		}
		if s.Status != domain.ReturnPendingDispatch {
			return false, apperror.Conflict("This parcel was already sent or closed")
		}
		at := in.DispatchedAt.UTC()
		s.Status, s.CarrierName, s.TrackingNumber, s.DispatchedAt, s.DispatchOperationID =
			domain.ReturnInTransit, &carrier, &tracking, &at, &in.OperationID
		return true, nil
	})
}

// Receive closes the parcel: the shop has the goods (Order recorded the
// receipt). A parcel handed over in person was never in transit.
func (uc *ReturnShipmentUseCase) Receive(ctx context.Context, id string) (*domain.ReturnShipment, error) {
	return uc.step(ctx, id, func(s *domain.ReturnShipment) (bool, error) {
		switch s.Status {
		case domain.ReturnReceived:
			return false, nil
		case domain.ReturnPendingDispatch, domain.ReturnInTransit:
			now := uc.now()
			s.Status, s.ReceivedAt = domain.ReturnReceived, &now
			return true, nil
		}
		return false, apperror.Conflict("This parcel is closed as a delivery exception")
	})
}

// Exception records that the parcel did not reach the shop (lost on the
// way back), after Order's admin checked with the carrier.
func (uc *ReturnShipmentUseCase) Exception(ctx context.Context, id, reason string) (*domain.ReturnShipment, error) {
	why, err := domain.ValidateNote(reason, true, "Reason")
	if err != nil {
		return nil, err
	}
	return uc.step(ctx, id, func(s *domain.ReturnShipment) (bool, error) {
		switch s.Status {
		case domain.ReturnDeliveryException:
			return false, nil
		case domain.ReturnPendingDispatch, domain.ReturnInTransit:
			now := uc.now()
			s.Status, s.ExceptionReason, s.ExceptionAt = domain.ReturnDeliveryException, why, &now
			return true, nil
		}
		return false, apperror.Conflict("This parcel was already received")
	})
}

func (uc *ReturnShipmentUseCase) step(ctx context.Context, id string, fn func(s *domain.ReturnShipment) (bool, error)) (*domain.ReturnShipment, error) {
	var out *domain.ReturnShipment
	changed := false
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		s, err := uc.Returns.FindByID(ctx, id)
		if err != nil {
			return err
		}
		out = s
		if changed, err = fn(s); err != nil || !changed {
			return err
		}
		return uc.Returns.Save(ctx, s)
	})
	if err != nil {
		return nil, returnError(err)
	}
	if changed {
		uc.Log.Info().Str("return_shipment_id", out.ID).Str("return_id", out.ReturnID).Str("status", string(out.Status)).Msg("shipment_return_status_changed")
	}
	return out, nil
}

// AdminList is the operators' view; status "stale" lists parcels in
// transit for too long.
func (uc *ReturnShipmentUseCase) AdminList(ctx context.Context, adminID, status string, limit, offset int) ([]*domain.ReturnShipment, error) {
	if uc.Identity == nil {
		return nil, apperror.Internal(errors.New("role verification is not configured"))
	}
	if err := uc.Identity.RequireRole(ctx, adminID, "admin"); err != nil {
		return nil, err
	}
	switch status {
	case "", "stale", string(domain.ReturnPendingDispatch), string(domain.ReturnInTransit), string(domain.ReturnReceived), string(domain.ReturnDeliveryException):
	default:
		return nil, apperror.Validation("Unknown status filter")
	}
	out, err := uc.Returns.List(ctx, status, limit, offset)
	return out, returnError(err)
}

// Report logs parcels waiting (warn when one is in transit too long).
func (uc *ReturnShipmentUseCase) Report(ctx context.Context) {
	pending, inTransit, stale, err := uc.Returns.Counts(ctx)
	if err != nil {
		if ctx.Err() == nil {
			uc.Log.Error().Err(err).Msg("shipment_return_report_failed")
		}
		return
	}
	if pending+inTransit == 0 {
		return
	}
	event := uc.Log.Info()
	if stale > 0 {
		event = uc.Log.Warn()
	}
	event.Int64("pending_dispatch", pending).Int64("in_transit", inTransit).Int64("in_transit_stale", stale).Msg("shipment_return_backlog")
}
