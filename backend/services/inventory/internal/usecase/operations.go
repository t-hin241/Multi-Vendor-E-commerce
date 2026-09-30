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
type Operations struct {
	Transactions Transactions
	Identity     Identity
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
