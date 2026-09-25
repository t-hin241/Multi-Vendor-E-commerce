// Package usecase orchestrates Notification's one workflow: given a user id
// and an event type, resolve who to reach and send them a message, keeping
// a record of the attempt either way. Callers (Order, Vendor) never wait on
// a queue for this in the current phase — they call it best-effort and log
// on error, since a notification failure must never roll back the order,
// payment or vendor decision that triggered it.
package usecase

import (
	"context"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/notification/internal/domain"
	"shopee/backend/services/notification/internal/sender"
)

type NotificationUseCase struct {
	notifications NotificationRepositoryPort
	identity      IdentityGateway
	sender        sender.Sender
	log           zerolog.Logger
}

func NewNotificationUseCase(notifications NotificationRepositoryPort, identity IdentityGateway, emailSender sender.Sender, log zerolog.Logger) *NotificationUseCase {
	return &NotificationUseCase{notifications: notifications, identity: identity, sender: emailSender, log: log}
}

// Notify resolves userID to an address, attempts delivery, and records the
// outcome. A failure to resolve the user or to send is recorded as a failed
// notification and returned as an error for the caller to log — it is never
// something the caller should treat as fatal to its own transaction.
func (uc *NotificationUseCase) Notify(ctx context.Context, userID string, notifType domain.Type, referenceID string) error {
	n := &domain.Notification{UserID: userID, Type: notifType, ReferenceID: referenceID, Status: domain.StatusSent}

	user, err := uc.identity.GetUser(ctx, userID)
	if err != nil {
		reason := "could not resolve recipient: " + err.Error()
		n.Status, n.FailReason = domain.StatusFailed, &reason
		uc.record(ctx, n)
		return apperror.Internal(err)
	}

	if err := uc.sender.Send(ctx, sender.Email{ToEmail: user.Email, ToName: user.FullName, Type: string(notifType), Reference: referenceID}); err != nil {
		reason := "send failed: " + err.Error()
		n.Status, n.FailReason = domain.StatusFailed, &reason
		uc.record(ctx, n)
		return apperror.Internal(err)
	}

	uc.record(ctx, n)
	return nil
}

func (uc *NotificationUseCase) record(ctx context.Context, n *domain.Notification) {
	if err := uc.notifications.Create(ctx, n); err != nil {
		uc.log.Error().Err(err).Str("user_id", n.UserID).Str("type", string(n.Type)).Msg("failed to persist notification record")
	}
}

func (uc *NotificationUseCase) List(ctx context.Context, limit, offset int) ([]*domain.Notification, error) {
	notifications, err := uc.notifications.List(ctx, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return notifications, nil
}
