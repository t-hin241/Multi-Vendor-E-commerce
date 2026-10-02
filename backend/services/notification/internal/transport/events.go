package transport

import (
	"context"

	"github.com/jackc/pgx/v5"

	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/services/notification/internal/domain"
	"shopee/backend/services/notification/internal/usecase"
)

// NotificationRequestedHandler records order.notification_requested and
// vendor.notification_requested events as notifications (the event id is
// the notification's event id, the producer its source). The notification
// is written in its own transaction so its delivery job runs on committed
// data; a redelivered event is a duplicate there too (dedup key).
func NotificationRequestedHandler(uc *usecase.NotificationUseCase) eventbus.Handler {
	return func(ctx context.Context, _ pgx.Tx, env eventbus.Envelope) error {
		var n events.NotificationRequest
		if err := env.Decode(&n); err != nil {
			return err
		}
		_, _, err := uc.Accept(ctx, domain.Request{EventID: env.EventID, Source: env.Producer, UserID: n.UserID,
			Type: domain.Type(n.Type), ReferenceID: n.ReferenceID, CorrelationID: env.CorrelationID})
		return err
	}
}
