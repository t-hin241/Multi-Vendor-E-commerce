package transport

import (
	"context"
	"fmt"

	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/services/notification/internal/domain"
	"shopee/backend/services/notification/internal/usecase"
)

// SLANotices (PW-045) is the casesla publisher of Notification's own
// deadlines: the owner and the deliverer are the same service, so a
// reminder is recorded as a notification directly (the outbox row id is
// the event id) instead of going through the event bus.
type SLANotices struct{ UseCase *usecase.NotificationUseCase }

func (s SLANotices) Publish(ctx context.Context, env eventbus.Envelope) error {
	var notice events.WorkItemNotice
	if err := env.Decode(&notice); err != nil {
		return err
	}
	if notice.ResourceType != "vendor_action" {
		return fmt.Errorf("unexpected deadline resource %q", notice.ResourceType)
	}
	_, _, err := s.UseCase.Accept(ctx, domain.Request{EventID: env.EventID, Source: "notification", UserID: notice.UserID,
		Type: domain.Type("sla_" + notice.ResourceType), ReferenceID: notice.ResourceID, CorrelationID: env.CorrelationID})
	return err
}
