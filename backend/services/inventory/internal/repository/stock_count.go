package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/inventory/internal/domain"
)

// FindByID returns one stock item.
func (r *InventoryItemRepository) FindByID(ctx context.Context, id string) (*domain.InventoryItem, error) {
	var item domain.InventoryItem
	err := connection(ctx, r.pool).QueryRow(ctx, `
		SELECT id, product_id, variant_id, vendor_id, available_quantity, reserved_quantity, created_at, updated_at
		FROM inventory_items WHERE id = $1`, id).Scan(
		&item.ID, &item.ProductID, &item.VariantID, &item.VendorID, &item.AvailableQuantity, &item.ReservedQuantity, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrItemNotFound
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// RecordStockCount applies a physical count to one item in a single
// transaction: the item row is locked, available is set from the count
// (reserved is never changed), and the count plus a stock movement are
// written. A retry with the same count id and payload returns the stored
// count (replayed=true) without applying it again; the same id with a
// different payload is a conflict.
func (r *InventoryItemRepository) RecordStockCount(ctx context.Context, count *domain.StockCount) (result *domain.StockCount, replayed bool, err error) {
	err = (Transactions{Pool: r.pool}).Run(ctx, func(ctx context.Context) error {
		q := connection(ctx, r.pool)
		var available, reserved int64
		err := q.QueryRow(ctx, `SELECT available_quantity, reserved_quantity FROM inventory_items WHERE id = $1 FOR UPDATE`, count.InventoryItemID).
			Scan(&available, &reserved)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrItemNotFound
		}
		if err != nil {
			return err
		}

		stored, err := r.findStockCount(ctx, count.ID)
		if err != nil {
			return err
		}
		if stored != nil {
			if !domain.SameStockCount(stored, count.InventoryItemID, count.CountedOnHand, count.Reason) {
				return apperror.Conflict("This stock count id was already used with different values")
			}
			result, replayed = stored, true
			return nil
		}

		next, err := domain.PlanStockCount(available, reserved, count.CountedOnHand)
		if err != nil {
			return err
		}
		count.PreviousAvailable, count.NewAvailable, count.ReservedAtCount = available, next, reserved
		if err := q.QueryRow(ctx, `
			INSERT INTO inventory_stock_counts (id, inventory_item_id, counted_on_hand, previous_available, new_available, reserved_at_count, actor_user_id, reason)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING created_at`,
			count.ID, count.InventoryItemID, count.CountedOnHand, available, next, reserved, count.ActorUserID, count.Reason,
		).Scan(&count.CreatedAt); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE inventory_items SET available_quantity = $2, updated_at = now() WHERE id = $1`, count.InventoryItemID, next); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO stock_movements (inventory_item_id, change_quantity, reserved_change, reason, reference_id, actor_user_id, operation_key, membership_version)
			VALUES ($1, $2, 0, 'stock_count', $3, $4, 'count:'||$3, $5)`,
			count.InventoryItemID, next-available, count.ID, count.ActorUserID, shopaccess.UsedMembershipVersion(ctx)); err != nil {
			return err
		}
		result = count
		return nil
	})
	return result, replayed, err
}

func (r *InventoryItemRepository) findStockCount(ctx context.Context, id string) (*domain.StockCount, error) {
	var c domain.StockCount
	err := connection(ctx, r.pool).QueryRow(ctx, `
		SELECT id, inventory_item_id, counted_on_hand, previous_available, new_available, reserved_at_count, actor_user_id, reason, created_at
		FROM inventory_stock_counts WHERE id = $1`, id).Scan(
		&c.ID, &c.InventoryItemID, &c.CountedOnHand, &c.PreviousAvailable, &c.NewAvailable, &c.ReservedAtCount, &c.ActorUserID, &c.Reason, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}
