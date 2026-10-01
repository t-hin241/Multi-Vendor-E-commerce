package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/domain"
)

// AdminConfig is what the shipping configuration use cases (carriers,
// zones, fee rules) need to change data: the admin re-verified with
// Identity, one transaction, and an audit row written in it.
type AdminConfig struct {
	Tx       Transactor
	Identity RoleVerifier
	Audit    AuditPort
}

// change verifies the admin, then runs fn and records the action it
// returns in the same transaction. It fails closed when any part is
// missing, so configuration never changes unverified or unaudited.
func (a AdminConfig) change(ctx context.Context, actorID string, fn func(ctx context.Context) (domain.AdminAction, error)) error {
	if a.Tx == nil || a.Identity == nil || a.Audit == nil {
		return apperror.Internal(errors.New("shipping configuration changes are not configured"))
	}
	if err := a.Identity.RequireRole(ctx, actorID, "admin"); err != nil {
		return mapError(err)
	}
	return mapError(a.Tx.Run(ctx, func(ctx context.Context) error {
		action, err := fn(ctx)
		if err != nil {
			return err
		}
		action.ActorID = actorID
		return a.Audit.Record(ctx, action)
	}))
}
