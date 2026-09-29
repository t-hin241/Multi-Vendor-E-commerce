package usecase

import (
	"context"
	"time"

	"shopee/backend/services/identity/internal/domain"
	"shopee/backend/services/identity/internal/repository"
)

// UserRepository provides account persistence and audit operations.
type UserRepository interface {
	Audit(context.Context, string, string, string, string) error
	Create(ctx context.Context, u *domain.User) error
	FindByEmail(ctx context.Context, email string) (*domain.User, error)
	FindByID(ctx context.Context, id string) (*domain.User, error)
	UpdatePasswordHash(ctx context.Context, userID, passwordHash string) error
	List(ctx context.Context, role, q string, limit, offset int) ([]*domain.User, error)
	SetActive(ctx context.Context, userID string, isActive bool) error
}

type RefreshTokenRepository interface {
	OwnerByHash(context.Context, string) (string, error)
	RevokeSession(context.Context, string, string) error
	SessionActive(context.Context, string, string, string) (bool, error)
	Create(ctx context.Context, userID, tokenHash string, expiresAt time.Time, familyID string) error
	FindActiveByHash(ctx context.Context, tokenHash string) (*repository.RefreshToken, error)
	Revoke(ctx context.Context, id string) error
	RevokeAllForUser(ctx context.Context, userID string) error
	RevokeByHash(ctx context.Context, tokenHash string) error
}

type PasswordResetRepository interface {
	QueueDelivery(context.Context, string, string, []byte, time.Time) error
	InvalidateForUser(context.Context, string) error
	Create(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error
	FindUsableByHash(ctx context.Context, tokenHash string) (*repository.PasswordResetToken, error)
	MarkUsed(ctx context.Context, id string) error
}
