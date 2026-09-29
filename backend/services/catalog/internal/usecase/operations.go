package usecase

import (
	"context"
	"errors"
	"github.com/rs/zerolog"
	"shopee/backend/pkg/apperror"
)

type Transactions interface {
	Run(context.Context, func(context.Context) error) error
}
type CleanupQueue interface {
	Track(context.Context, string, string) error
	Enqueue(context.Context, string, string) error
}
type RoleVerifier interface {
	RequireRole(context.Context, string, string) error
}
type Operations struct {
	Identity     RoleVerifier
	Transactions Transactions
	Cleanup      CleanupQueue
	Log          zerolog.Logger
}

func (uc *ProductUseCase) transact(ctx context.Context, fn func(context.Context) error) error {
	err := uc.ops.Transactions.Run(ctx, fn)
	var app *apperror.Error
	if err == nil || errors.As(err, &app) {
		return err
	}
	return apperror.Internal(err)
}

func (uc *ProductUseCase) observe(ctx context.Context, operation string, err error) {
	if err != nil {
		uc.ops.Log.Warn().Err(err).Str("operation", operation).Msg("catalog_dependency_degraded")
	}
}
