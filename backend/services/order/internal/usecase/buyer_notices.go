package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/events"
	"shopee/backend/pkg/noticeoutbox"
)

// Buyer notices without an order (PW-009); the reference is the request id.
const (
	noticeIntakeReceived = "support_intake_received"
	noticeIntakeClosed   = "support_intake_closed"
)

// BuyerNoticePort is the outbox of buyer notices that have no order effect
// to ride on (repository.BuyerNotices).
type BuyerNoticePort interface {
	Queue(ctx context.Context, dedupKey, userID, noticeType, referenceID string) error
	Drain(ctx context.Context, limit int, deliver func(context.Context, noticeoutbox.Notice) error) (int, error)
	Pending(ctx context.Context) (waiting, review int64, err error)
}

// queueBuyerNotice records the notice in the caller's transaction, once
// per (type, reference). Without an outbox nothing is sent, as before.
func (uc *OrderUseCase) queueBuyerNotice(ctx context.Context, userID, noticeType, referenceID string) error {
	if uc.BuyerNotices == nil {
		return nil
	}
	return uc.BuyerNotices.Queue(ctx, noticeType+":"+referenceID, userID, noticeType, referenceID)
}

// RelayBuyerNotices sends due notices to Notification: an event with the
// event bus, otherwise the HTTP gateway. The row id is the event id.
func (uc *OrderUseCase) RelayBuyerNotices(ctx context.Context, limit int) (int, error) {
	if uc.BuyerNotices == nil {
		return 0, nil
	}
	return uc.BuyerNotices.Drain(ctx, limit, func(ctx context.Context, n noticeoutbox.Notice) error {
		req := events.NotificationRequest{UserID: n.UserID, Type: n.Type, ReferenceID: n.ReferenceID}
		if uc.Events != nil {
			env, err := events.OrderNotification(n.ID, n.ReferenceID, req)
			if err != nil {
				return err
			}
			return uc.Events.Publish(ctx, env.WithCorrelation(n.ID))
		}
		if uc.Notifications == nil {
			return errors.New("notifications are not wired")
		}
		return uc.Notifications.Notify(ctx, n.ID, n.UserID, n.Type, n.ReferenceID)
	})
}

func (uc *OrderUseCase) reportBuyerNotices(ctx context.Context) {
	if uc.BuyerNotices == nil {
		return
	}
	waiting, review, err := uc.BuyerNotices.Pending(ctx)
	if err != nil {
		if ctx.Err() == nil {
			uc.Log.Error().Err(err).Msg("order_buyer_notice_stats_failed")
		}
		return
	}
	if review > 0 {
		uc.Log.Warn().Int64("waiting", waiting).Int64("needs_review", review).Msg("order_buyer_notices_need_review")
	}
}
