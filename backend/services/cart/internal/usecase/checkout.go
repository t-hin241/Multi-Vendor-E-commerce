package usecase

import (
	"context"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/cart/internal/domain"
)

// CreateCheckoutSnapshot freezes the buyer's cart for one checkout
// operation. Repeating the call with the same operation id returns the
// stored snapshot (replayed=true) even if the cart changed since, so an
// Order retry prices exactly what it priced the first time. When
// expectedVersion is given and the operation is new, a cart that moved on
// is rejected with cart_changed so the buyer reviews it again.
func (uc *CartUseCase) CreateCheckoutSnapshot(ctx context.Context, buyerID, operationID string, expectedVersion *int64) (op *domain.CheckoutOperation, replayed bool, err error) {
	err = uc.tx.Run(ctx, func(ctx context.Context) error {
		cart, err := uc.carts.LockForUser(ctx, buyerID)
		if err != nil {
			return err
		}
		existing, err := uc.operations.Find(ctx, operationID)
		if err != nil {
			return err
		}
		if existing != nil {
			if existing.BuyerID != buyerID {
				return apperror.Conflict("Checkout operation belongs to a different buyer")
			}
			op, replayed = existing, true
			return nil
		}
		if err := domain.CheckExpectedVersion(expectedVersion, cart.Version); err != nil {
			return err
		}
		items, err := uc.items.ListByCart(ctx, cart.ID)
		if err != nil {
			return err
		}
		lines, err := domain.SnapshotFromItems(items)
		if err != nil {
			return err
		}
		op = &domain.CheckoutOperation{OperationID: operationID, BuyerID: buyerID, CartID: cart.ID, CartVersion: cart.Version, Lines: lines}
		return uc.operations.Insert(ctx, op)
	})
	if err != nil {
		return nil, false, asAppError(err)
	}
	return op, replayed, nil
}

// ConsumeCheckout removes from the cart exactly the purchased units of the
// snapshotted lines, once. Lines added after the snapshot, units added to a
// snapshotted line after the snapshot, and lines not named are kept. A
// repeated call with the same lines returns the stored receipt
// (replayed=true); a repeated call with different lines is a conflict.
func (uc *CartUseCase) ConsumeCheckout(ctx context.Context, buyerID, operationID string, lines []domain.ConsumeLine) (receipt *domain.ConsumeReceipt, replayed bool, err error) {
	hash := domain.ConsumeHash(lines)
	err = uc.tx.Run(ctx, func(ctx context.Context) error {
		// Locking the buyer's cart serializes consume with every cart
		// mutation, so a concurrent add/update is either fully before or
		// fully after it.
		cart, err := uc.carts.LockForUser(ctx, buyerID)
		if err != nil {
			return err
		}
		op, err := uc.operations.Find(ctx, operationID)
		if err != nil {
			return err
		}
		if op == nil {
			return apperror.NotFound("Checkout operation not found")
		}
		if op.BuyerID != buyerID {
			return apperror.Conflict("Checkout operation belongs to a different buyer")
		}
		if op.Consumed() {
			if op.ConsumeHash != nil && *op.ConsumeHash == hash {
				receipt, replayed = op.Receipt, true
				return nil
			}
			uc.log.Warn().Str("operation_id", operationID).Msg("cart_consume_conflict")
			return apperror.Conflict("Checkout operation was already consumed with different lines")
		}
		if err := domain.ValidateConsume(op.Lines, lines); err != nil {
			return err
		}

		current := map[string]*domain.CartItem{}
		// A purged-and-recreated cart has a new id: none of the snapshotted
		// lines exist any more, and nothing in the new cart is touched.
		if op.CartID == cart.ID {
			ids := make([]string, 0, len(lines))
			for _, l := range lines {
				ids = append(ids, l.LineID)
			}
			items, err := uc.items.ListByIDs(ctx, cart.ID, ids)
			if err != nil {
				return err
			}
			for _, item := range items {
				current[item.ID] = item
			}
		}
		quantities := make(map[string]int64, len(current))
		for id, item := range current {
			quantities[id] = item.Quantity
		}

		results := domain.PlanConsume(quantities, lines)
		changed := false
		for _, r := range results {
			switch r.Outcome {
			case domain.ConsumeRemoved:
				if _, err := uc.items.Delete(ctx, cart.ID, r.LineID); err != nil {
					return err
				}
				changed = true
			case domain.ConsumeReduced:
				item := current[r.LineID]
				item.Quantity = r.RemainingQuantity
				if err := uc.items.Update(ctx, item); err != nil {
					return err
				}
				changed = true
			}
		}
		version := cart.Version
		if changed {
			if version, err = uc.carts.BumpVersion(ctx, cart.ID); err != nil {
				return err
			}
		}
		receipt = &domain.ConsumeReceipt{CartVersion: version, Lines: results, ConsumedAt: time.Now().UTC()}
		return uc.operations.MarkConsumed(ctx, operationID, hash, receipt)
	})
	if err != nil {
		return nil, false, asAppError(err)
	}
	return receipt, replayed, nil
}
