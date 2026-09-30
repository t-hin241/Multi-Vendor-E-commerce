package usecase

import (
	"context"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/cart/internal/domain"
)

// CurrentLines returns the buyer's cart lines and version without creating
// a checkout operation. Order uses it to price a checkout preview; the real
// checkout still takes a snapshot.
func (uc *CartUseCase) CurrentLines(ctx context.Context, buyerID string) (int64, []domain.SnapshotLine, error) {
	cart, err := uc.carts.GetOrCreateForUser(ctx, buyerID)
	if err != nil {
		return 0, nil, apperror.Internal(err)
	}
	items, err := uc.items.ListByCart(ctx, cart.ID)
	if err != nil {
		return 0, nil, apperror.Internal(err)
	}
	if len(items) == 0 {
		return cart.Version, []domain.SnapshotLine{}, nil
	}
	lines, err := domain.SnapshotFromItems(items)
	if err != nil {
		return 0, nil, err
	}
	return cart.Version, lines, nil
}
