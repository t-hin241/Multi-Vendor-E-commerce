package transport

import (
	"context"

	"github.com/jackc/pgx/v5"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
	"shopee/backend/services/shipment/internal/usecase"
)

// FulfillmentReadyHandler opens the shipment of a paid vendor order from
// order.fulfillment_ready, in the inbox transaction (a shipment that
// already exists for the vendor order is kept).
func FulfillmentReadyHandler(shipments *usecase.ShipmentUseCase) eventbus.Handler {
	return func(ctx context.Context, tx pgx.Tx, env eventbus.Envelope) error {
		var f events.Fulfillment
		if err := env.Decode(&f); err != nil {
			return err
		}
		if f.VendorOrderID == "" || f.VendorID == "" || f.BuyerID == "" || f.RecipientName == "" || f.Phone == "" ||
			f.Province == "" || f.StreetAddress == "" {
			return apperror.Validation("Fulfillment event is missing required fields")
		}
		in := usecase.CreateShipmentInput{
			VendorOrderID: f.VendorOrderID, VendorID: f.VendorID, BuyerID: f.BuyerID, PackageWeightGrams: f.PackageWeightGrams,
			RecipientName: f.RecipientName, Phone: f.Phone, Province: f.Province, District: f.District, Ward: f.Ward, StreetAddress: f.StreetAddress,
		}
		if q := f.Quote; q != nil {
			if q.FeeAmount < 0 || q.CarrierID == "" || q.ZoneID == "" || q.FeeRuleID == "" {
				return apperror.Validation("Fulfillment event has an incomplete fee quote")
			}
			in.Quote = &domain.QuotedFee{FeeAmount: q.FeeAmount, CarrierID: q.CarrierID, ZoneID: q.ZoneID, FeeRuleID: q.FeeRuleID}
		}
		_, err := shipments.CreateAuto(repository.WithTx(ctx, tx), in)
		return err
	}
}

// FulfillmentCancelledHandler stops the shipment of a cancelled vendor
// order (order.fulfillment_cancelled).
func FulfillmentCancelledHandler(shipments *usecase.ShipmentUseCase) eventbus.Handler {
	return func(ctx context.Context, tx pgx.Tx, env eventbus.Envelope) error {
		var c events.FulfillmentCancellation
		if err := env.Decode(&c); err != nil {
			return err
		}
		if c.VendorOrderID == "" {
			return apperror.Validation("Cancellation event names no vendor order")
		}
		return shipments.CancelForVendorOrder(repository.WithTx(ctx, tx), c.VendorOrderID)
	}
}
