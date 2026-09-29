package usecase

import (
	"context"

	"shopee/backend/services/notification/internal/domain"
)

type ResetMessage = domain.ResetMessage
type ResetMessageSource interface {
	GetResetMessage(context.Context, string) (*ResetMessage, error)
}
type ResetMailSender interface {
	SendReset(context.Context, *ResetMessage) error
}
type PasswordResetUseCase struct {
	Source ResetMessageSource
	Sender ResetMailSender
}

// Neither the message nor the token is persisted in Notification.
func (u *PasswordResetUseCase) Deliver(ctx context.Context, id string) error {
	message, err := u.Source.GetResetMessage(ctx, id)
	if err != nil {
		return err
	}
	return u.Sender.SendReset(ctx, message)
}
