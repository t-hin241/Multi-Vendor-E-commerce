package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/inventory/internal/domain"
)

type InventoryItemRepository struct {
	pool *pgxpool.Pool
}

func NewInventoryItemRepository(pool *pgxpool.Pool) *InventoryItemRepository {
	return &InventoryItemRepository{pool: pool}
}

func (r *InventoryItemRepository) Create(ctx context.Context, item *domain.InventoryItem) error {
	const query = `
		INSERT INTO inventory_items (product_id, variant_id, vendor_id, available_quantity)
		VALUES ($1, $2, $3, $4)
		RETURNING id, reserved_quantity, created_at, updated_at`

	err := r.pool.QueryRow(ctx, query, item.ProductID, item.VariantID, item.VendorID, item.AvailableQuantity).
		Scan(&item.ID, &item.ReservedQuantity, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrItemAlreadyExists
		}
		return err
	}

	const movementQuery = `INSERT INTO stock_movements (inventory_item_id, change_quantity, reason) VALUES ($1, $2, 'initial_stock')`
	_, err = r.pool.Exec(ctx, movementQuery, item.ID, item.AvailableQuantity)
	return err
}

func (r *InventoryItemRepository) FindByProductID(ctx context.Context, productID string) (*domain.InventoryItem, error) {
	const query = `
		SELECT id, product_id, variant_id, vendor_id, available_quantity, reserved_quantity, created_at, updated_at
		FROM inventory_items WHERE product_id = $1 AND variant_id IS NULL`

	var item domain.InventoryItem
	err := r.pool.QueryRow(ctx, query, productID).Scan(
		&item.ID, &item.ProductID, &item.VariantID, &item.VendorID, &item.AvailableQuantity, &item.ReservedQuantity, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrItemNotFound
		}
		return nil, err
	}
	return &item, nil
}

func (r *InventoryItemRepository) FindByVariantID(ctx context.Context, variantID string) (*domain.InventoryItem, error) {
	const query = `
		SELECT id, product_id, variant_id, vendor_id, available_quantity, reserved_quantity, created_at, updated_at
		FROM inventory_items WHERE variant_id = $1`

	var item domain.InventoryItem
	err := r.pool.QueryRow(ctx, query, variantID).Scan(
		&item.ID, &item.ProductID, &item.VariantID, &item.VendorID, &item.AvailableQuantity, &item.ReservedQuantity, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrItemNotFound
		}
		return nil, err
	}
	return &item, nil
}

// ListByVariantIDs batch-looks-up current stock for several variants at
// once, keyed by variant id — backs the public per-variant stock read that
// Catalog's product-detail page calls. A variant missing from the result
// has no inventory row yet (not stocked) rather than an error.
func (r *InventoryItemRepository) ListByVariantIDs(ctx context.Context, variantIDs []string) (map[string]int64, error) {
	if len(variantIDs) == 0 {
		return map[string]int64{}, nil
	}
	const query = `SELECT variant_id, available_quantity FROM inventory_items WHERE variant_id = ANY($1)`
	rows, err := r.pool.Query(ctx, query, variantIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]int64, len(variantIDs))
	for rows.Next() {
		var variantID string
		var qty int64
		if err := rows.Scan(&variantID, &qty); err != nil {
			return nil, err
		}
		out[variantID] = qty
	}
	return out, rows.Err()
}

func (r *InventoryItemRepository) ListByVendor(ctx context.Context, vendorID string, limit, offset int) ([]*domain.InventoryItem, error) {
	const query = `
		SELECT id, product_id, variant_id, vendor_id, available_quantity, reserved_quantity, created_at, updated_at
		FROM inventory_items WHERE vendor_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`

	rows, err := r.pool.Query(ctx, query, vendorID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*domain.InventoryItem
	for rows.Next() {
		var item domain.InventoryItem
		if err := rows.Scan(&item.ID, &item.ProductID, &item.VariantID, &item.VendorID, &item.AvailableQuantity, &item.ReservedQuantity, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, &item)
	}
	return items, rows.Err()
}

// Restock adds quantity to available stock and records the movement in one
// transaction, so the ledger and the balance can never drift apart.
func (r *InventoryItemRepository) Restock(ctx context.Context, productID string, quantity int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var itemID string
	err = tx.QueryRow(ctx, `SELECT id FROM inventory_items WHERE product_id = $1 AND variant_id IS NULL FOR UPDATE`, productID).Scan(&itemID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &ErrProductNotStocked{ProductID: productID}
		}
		return err
	}

	if _, err := tx.Exec(ctx, `UPDATE inventory_items SET available_quantity = available_quantity + $1, updated_at = now() WHERE id = $2`, quantity, itemID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO stock_movements (inventory_item_id, change_quantity, reason) VALUES ($1, $2, 'restock')`, itemID, quantity); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// RestockVariant is Restock's sibling for a variant-scoped stock item.
func (r *InventoryItemRepository) RestockVariant(ctx context.Context, variantID string, quantity int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var itemID string
	err = tx.QueryRow(ctx, `SELECT id FROM inventory_items WHERE variant_id = $1 FOR UPDATE`, variantID).Scan(&itemID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &ErrProductNotStocked{ProductID: variantID}
		}
		return err
	}

	if _, err := tx.Exec(ctx, `UPDATE inventory_items SET available_quantity = available_quantity + $1, updated_at = now() WHERE id = $2`, quantity, itemID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO stock_movements (inventory_item_id, change_quantity, reason) VALUES ($1, $2, 'restock')`, itemID, quantity); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
