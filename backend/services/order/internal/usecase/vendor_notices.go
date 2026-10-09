package usecase

import (
	"context"
	"encoding/json"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/services/order/internal/domain"
)

// VendorNoticeGateway reports shop work to Notification over HTTP
// (EVENT_PUBLISHING=http); with the event bus the effect is an event.
type VendorNoticeGateway interface {
	NotifyVendorAction(ctx context.Context, eventID string, a events.VendorOrderAction) error
}

// errVendorNoticesNotWired keeps the effect retrying instead of dropping it.
var errVendorNoticesNotWired = errors.New("vendor notices are not wired")

// noticeVendor queues "this shop has work" (AF-08) in the caller's
// transaction, only with FEATURE_VENDOR_ACTION_NOTICES_ENABLED. Notification
// decides who in the shop is told; nothing here waits for it.
func (uc *OrderUseCase) noticeVendor(ctx context.Context, orderID, vendorID, vendorOrderID, kind, referenceID string) error {
	if !uc.VendorActionNotices {
		return nil
	}
	return uc.Effects.Enqueue(ctx, domain.NewVendorNoticeEffect(orderID, vendorID, vendorOrderID, kind, referenceID))
}

// noticeVendorOfReturn finds the shop of a return's item and queues kind.
func (uc *OrderUseCase) noticeVendorOfReturn(ctx context.Context, rr *domain.ReturnRequest, kind string) error {
	if !uc.VendorActionNotices {
		return nil
	}
	item, err := uc.Orders.FindItem(ctx, rr.OrderID, rr.OrderItemID)
	if err != nil {
		return err
	}
	vo, err := uc.VendorOrders.FindByID(ctx, item.VendorOrderID)
	if err != nil {
		return err
	}
	return uc.noticeVendor(ctx, rr.OrderID, vo.VendorID, vo.ID, kind, rr.ID)
}

// notifyVendor carries out a notify_vendor effect: the effect id is the
// event id, so a retry is the same notice at Notification.
func (uc *OrderUseCase) notifyVendor(ctx context.Context, e *domain.Effect) error {
	var p domain.VendorNoticePayload
	if err := json.Unmarshal(e.Payload, &p); err != nil || p.VendorID == "" || p.ActionKind == "" {
		return apperror.Validation("invalid vendor notice payload")
	}
	a := events.VendorOrderAction{VendorID: p.VendorID, VendorOrderID: p.VendorOrderID, OrderID: e.OrderID, ActionKind: p.ActionKind, ReferenceID: p.ReferenceID}
	if uc.Events != nil {
		return uc.publish(ctx, e, func() (eventbus.Envelope, error) { return events.VendorOrderActionEvent(e.ID, a) })
	}
	if uc.VendorNotices == nil {
		return apperror.Internal(errVendorNoticesNotWired)
	}
	return uc.VendorNotices.NotifyVendorAction(ctx, e.ID, a)
}
