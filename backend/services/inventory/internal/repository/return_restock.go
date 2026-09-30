package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"shopee/backend/pkg/apperror"
)

// RestockReturn puts the units of one received return back into available
// stock with a 'return_restock' movement. The movement's operation key
// 'return:<id>' makes it happen once per return: a replay with the same
// item and quantity is acknowledged (replayed=true), anything else under
// that return id is a conflict.
func (r *InventoryItemRepository) RestockReturn(ctx context.Context, returnID, productID string, variantID *string, quantity int64) (replayed bool, err error) {
	if quantity <= 0 {
		return false, apperror.Validation("Return quantity must be positive")
	}
	key := "return:" + returnID
	err = (Transactions{Pool: r.pool}).Run(ctx, func(ctx context.Context) error {
		q := connection(ctx, r.pool)
		var itemID, itemProduct string
		var err error
		if variantID != nil {
			err = q.QueryRow(ctx, `SELECT id, product_id FROM inventory_items WHERE variant_id = $1 FOR UPDATE`, *variantID).Scan(&itemID, &itemProduct)
		} else {
			err = q.QueryRow(ctx, `SELECT id, product_id FROM inventory_items WHERE product_id = $1 AND variant_id IS NULL FOR UPDATE`, productID).Scan(&itemID, &itemProduct)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return &ErrProductNotStocked{ProductID: productID}
		}
		if err != nil {
			return err
		}
		if itemProduct != productID {
			return apperror.Conflict("Variant does not belong to the returned product")
		}

		var doneItem string
		var doneQty int64
		err = q.QueryRow(ctx, `SELECT inventory_item_id, change_quantity FROM stock_movements WHERE operation_key = $1 LIMIT 1`, key).Scan(&doneItem, &doneQty)
		if err == nil {
			if doneItem != itemID || doneQty != quantity {
				return apperror.Conflict("This return was already restocked with different values")
			}
			replayed = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		tag, err := q.Exec(ctx, `UPDATE inventory_items SET available_quantity = available_quantity + $1, updated_at = now()
			WHERE id = $2 AND available_quantity::numeric + reserved_quantity::numeric + $1::numeric <= 9223372036854775807`, quantity, itemID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return apperror.Validation("Stock quantity exceeds the supported maximum")
		}
		_, err = q.Exec(ctx, `INSERT INTO stock_movements (inventory_item_id, change_quantity, reason, reference_id, operation_key)
			VALUES ($1, $2, 'return_restock', $3, $4)`, itemID, quantity, returnID, key)
		return err
	})
	return replayed, err
}
