package usecase

import (
	"context"
	"time"

	"shopee/backend/services/notification/internal/adapter"
	"shopee/backend/services/notification/internal/domain"
	"shopee/backend/services/notification/internal/repository"
)

// Store is the durable notification queue.
type Store interface {
	Insert(ctx context.Context, n *domain.Notification) (*domain.Notification, bool, error)
	FindByID(ctx context.Context, id string) (*domain.Notification, error)
	ClaimTask(ctx context.Context, id string, attempt int, lease time.Duration) (*domain.Notification, error)
	ParkStuck(ctx context.Context) (int64, error)
	Unqueued(ctx context.Context, grace time.Duration, limit int) ([]repository.Due, error)
	Finish(ctx context.Context, id string, attempt int, o repository.Outcome) error
	List(ctx context.Context, f repository.Filter, limit, offset int) ([]*domain.Notification, int64, error)
	Attempts(ctx context.Context, id string) ([]*domain.Attempt, error)
	Requeue(ctx context.Context, id string, extraAttempts int) (*domain.Notification, error)
	RecordAudit(ctx context.Context, actorID, action, id string, reason *string, changes map[string]any) error
	Counts(ctx context.Context) (map[string]int64, error)
	PurgeAttempts(ctx context.Context, before time.Time) (int64, error)
}

// TaskQueue carries the job "make attempt N of notification X" (Asynq on
// Redis): it wakes a worker at the right time. PostgreSQL stays the record,
// so a lost job is queued again from it (Recover). Enqueue keeps a job
// already queued for the same attempt and reports whether it added one.
type TaskQueue interface {
	Enqueue(ctx context.Context, id string, attempt int, at time.Time) (bool, error)
	Stats(ctx context.Context) (map[string]int64, error)
}

// IdentityGateway resolves a user id to an address/name, without
// Notification owning any account data itself.
type IdentityGateway interface {
	GetUser(ctx context.Context, userID string) (*adapter.UserSnapshot, error)
}

// RoleVerifier re-verifies an admin with Identity.
type RoleVerifier interface {
	RequireRole(ctx context.Context, userID, role string) error
}

// Transactor runs fn in one database transaction.
type Transactor interface {
	Run(ctx context.Context, fn func(context.Context) error) error
}
