package usecase

import (
	"context"
	"time"

	"shopee/backend/services/identity/internal/domain"
	"shopee/backend/services/identity/internal/repository"
)

// These interfaces let the use case depend on behavior, not on pgx, so its
// business logic (duplicate-email handling, generic auth error messages,
// refresh-token rotation) can be unit-tested with in-memory fakes instead of
// a live database.

type UserRepository interface {
	Create(ctx context.Context, u *domain.User) error
	FindByEmail(ctx context.Context, email string) (*domain.User, error)
	FindByID(ctx context.Context, id string) (*domain.User, error)
	UpdatePasswordHash(ctx context.Context, userID, passwordHash string) error
}

type RefreshTokenRepository interface {
	Create(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error
	FindActiveByHash(ctx context.Context, tokenHash string) (*repository.RefreshToken, error)
	Revoke(ctx context.Context, id string) error
	RevokeAllForUser(ctx context.Context, userID string) error
	RevokeByHash(ctx context.Context, tokenHash string) error
}

type PasswordResetRepository interface {
	Create(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error
	FindUsableByHash(ctx context.Context, tokenHash string) (*repository.PasswordResetToken, error)
	MarkUsed(ctx context.Context, id string) error
}
