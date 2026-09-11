package usecase

import (
	"context"

	"shopee/backend/services/notification/internal/adapter"
	"shopee/backend/services/notification/internal/domain"
)

type NotificationRepositoryPort interface {
	Create(ctx context.Context, n *domain.Notification) error
	List(ctx context.Context, limit, offset int) ([]*domain.Notification, error)
}

// IdentityGateway resolves a user id to an address/name, without
// Notification owning any account data itself.
type IdentityGateway interface {
	GetUser(ctx context.Context, userID string) (*adapter.UserSnapshot, error)
}
