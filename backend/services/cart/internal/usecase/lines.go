package usecase

import (
	"context"

	"shopee/backend/services/cart/internal/domain"
)

// CurrentLines reads version and items under the same lock as cart mutations.
func (uc *CartUseCase) CurrentLines(ctx context.Context, buyerID string) (version int64, lines []domain.SnapshotLine, err error) {
	err = uc.tx.Run(ctx, func(ctx context.Context) error {
		cart, e := uc.carts.LockForUser(ctx, buyerID)
		if e != nil {
			return e
		}
		items, e := uc.items.ListByCart(ctx, cart.ID)
		if e != nil {
			return e
		}
		version = cart.Version
		lines = make([]domain.SnapshotLine, 0, len(items))
		// Preview can select valid lines even in an old over-limit cart.
		for _, item := range items {
			lines = append(lines, domain.SnapshotLine{LineID: item.ID, ProductID: item.ProductID, VariantID: item.VariantID, Quantity: item.Quantity, LineVersion: item.Version, SeenPriceAmount: item.SeenPriceAmount, SeenCurrency: item.SeenCurrency, SeenPricingVersion: item.SeenPricingVersion})
		}
		return nil
	})
	if err != nil {
		return 0, nil, asAppError(err)
	}
	return version, lines, nil
}
