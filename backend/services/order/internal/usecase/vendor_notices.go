package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

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

// noticeVendorAgain is noticeVendor for work that can come back: each
// round (a deadline version, a case version) is its own notice.
func (uc *OrderUseCase) noticeVendorAgain(ctx context.Context, orderID, vendorID, vendorOrderID, kind, referenceID, round string) error {
	if !uc.VendorActionNotices {
		return nil
	}
	e := domain.NewVendorNoticeEffect(orderID, vendorID, vendorOrderID, kind, referenceID)
	e.Target += ":" + round
	return uc.Effects.Enqueue(ctx, e)
}

// NoticeShopDeadline (PW-009, AF-07) tells the shop that a deadline it must
// meet reached its reminder ("reminder") or passed ("overdue"). It runs in
// the SLA scan transaction (ctx carries it); stage names the shop's work:
// answering a support case or recording the goods of a failed delivery.
func (uc *OrderUseCase) NoticeShopDeadline(ctx context.Context, stage, resourceID string, deadlineVersion int64, kind string) error {
	if !uc.VendorActionNotices {
		return nil
	}
	round := "d" + strconv.FormatInt(deadlineVersion, 10)
	overdue := kind == "overdue"
	switch stage {
	case "vendor_response":
		c, err := uc.Support.FindByID(ctx, resourceID)
		if err != nil {
			return err
		}
		action := events.VendorActionSupportReplyDue
		if overdue {
			action = events.VendorActionSupportReplyOverdue
		}
		return uc.noticeVendorAgain(ctx, c.OrderID, c.VendorID, c.VendorOrderID, action, c.ID, round)
	case "delivery_goods_receipt":
		if uc.DeliveryExceptions == nil {
			return nil
		}
		d, err := uc.DeliveryExceptions.FindByID(ctx, resourceID)
		if err != nil {
			return err
		}
		action := events.VendorActionGoodsReceiptDue
		if overdue {
			action = events.VendorActionGoodsReceiptOverdue
		}
		return uc.noticeVendorAgain(ctx, d.OrderID, d.VendorID, d.VendorOrderID, action, d.ID, round)
	}
	return nil
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
