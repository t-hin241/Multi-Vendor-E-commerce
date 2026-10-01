package usecase

import (
	"context"
	"errors"
	"shopee/backend/pkg/apperror"
	"shopee/backend/services/inventory/internal/domain"
)

type Transactions interface {
	Run(context.Context, func(context.Context) error) error
}
type Identity interface {
	RequireRole(context.Context, string, string) error
}

// Audit records an admin decision in the caller's transaction.
type Audit interface {
	Record(ctx context.Context, entityType, entityID, actor, action string, reason *string, changes map[string]any) error
}

type Operations struct {
	Transactions Transactions
	Identity     Identity
	Audit        Audit
}

// audit fails closed: without an audit store an admin decision is refused.
func (o Operations) audit(ctx context.Context, entityType, entityID, actor, action string, reason *string, changes map[string]any) error {
	if o.Audit == nil {
		return apperror.Internal(errors.New("admin audit is not configured"))
	}
	return o.Audit.Record(ctx, entityType, entityID, actor, action, reason, changes)
}

func inventoryError(err error) error {
	if err == nil {
		return nil
	}
	var app *apperror.Error
	if errors.As(err, &app) {
		return err
	}
	return apperror.Internal(err)
}
func (uc *InventoryUseCase) Operation(ctx context.Context, id string) (*domain.Operation, error) {
	o, err := uc.reservations.Operation(ctx, id)
	return o, inventoryError(err)
}
