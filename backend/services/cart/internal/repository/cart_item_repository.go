package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/cart/internal/domain"
)

type CartItemRepository struct {
	pool *pgxpool.Pool
}

func NewCartItemRepository(pool *pgxpool.Pool) *CartItemRepository {
	return &CartItemRepository{pool: pool}
}

// AddQuantity increments the item's quantity, inserting a new row the first
// time a product (with no variant) is added to the cart.
func (r *CartItemRepository) AddQuantity(ctx context.Context, cartID, productID string, delta int64) error {
	const query = `
		INSERT INTO cart_items (cart_id, product_id, quantity)
		VALUES ($1, $2, $3)
		ON CONFLICT (cart_id, product_id) WHERE variant_id IS NULL
		DO UPDATE SET quantity = cart_items.quantity + EXCLUDED.quantity, updated_at = now()`
	_, err := r.pool.Exec(ctx, query, cartID, productID, delta)
	return err
}

// AddQuantityForVariant is AddQuantity's sibling for a variant-scoped line
// — a separate method/query (not a nullable-parameter branch) because the
// two cases target different partial unique indexes, matching the
// convention already established by Inventory's Restock/RestockVariant.
func (r *CartItemRepository) AddQuantityForVariant(ctx context.Context, cartID, productID, variantID string, delta int64) error {
	const query = `
		INSERT INTO cart_items (cart_id, product_id, variant_id, quantity)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (cart_id, variant_id) WHERE variant_id IS NOT NULL
		DO UPDATE SET quantity = cart_items.quantity + EXCLUDED.quantity, updated_at = now()`
	_, err := r.pool.Exec(ctx, query, cartID, productID, variantID, delta)
	return err
}

// SetQuantity sets an absolute quantity, removing the item if quantity is 0.
func (r *CartItemRepository) SetQuantity(ctx context.Context, cartID, productID string, quantity int64) error {
	if quantity == 0 {
		return r.Remove(ctx, cartID, productID)
	}

	const query = `
		INSERT INTO cart_items (cart_id, product_id, quantity)
		VALUES ($1, $2, $3)
		ON CONFLICT (cart_id, product_id) WHERE variant_id IS NULL
		DO UPDATE SET quantity = EXCLUDED.quantity, updated_at = now()`
	_, err := r.pool.Exec(ctx, query, cartID, productID, quantity)
	return err
}

// SetQuantityForVariant is SetQuantity's sibling for a variant-scoped line.
func (r *CartItemRepository) SetQuantityForVariant(ctx context.Context, cartID, productID, variantID string, quantity int64) error {
	if quantity == 0 {
		return r.RemoveVariant(ctx, cartID, variantID)
	}

	const query = `
		INSERT INTO cart_items (cart_id, product_id, variant_id, quantity)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (cart_id, variant_id) WHERE variant_id IS NOT NULL
		DO UPDATE SET quantity = EXCLUDED.quantity, updated_at = now()`
	_, err := r.pool.Exec(ctx, query, cartID, productID, variantID, quantity)
	return err
}

func (r *CartItemRepository) Remove(ctx context.Context, cartID, productID string) error {
	const query = `DELETE FROM cart_items WHERE cart_id = $1 AND product_id = $2 AND variant_id IS NULL`
	_, err := r.pool.Exec(ctx, query, cartID, productID)
	return err
}

// RemoveVariant is Remove's sibling for a variant-scoped line.
func (r *CartItemRepository) RemoveVariant(ctx context.Context, cartID, variantID string) error {
	const query = `DELETE FROM cart_items WHERE cart_id = $1 AND variant_id = $2`
	_, err := r.pool.Exec(ctx, query, cartID, variantID)
	return err
}

func (r *CartItemRepository) ListByCart(ctx context.Context, cartID string) ([]*domain.CartItem, error) {
	const query = `
		SELECT id, cart_id, product_id, variant_id, quantity, created_at, updated_at
		FROM cart_items WHERE cart_id = $1 ORDER BY created_at ASC`

	rows, err := r.pool.Query(ctx, query, cartID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*domain.CartItem
	for rows.Next() {
		var item domain.CartItem
		if err := rows.Scan(&item.ID, &item.CartID, &item.ProductID, &item.VariantID, &item.Quantity, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, &item)
	}
	return items, rows.Err()
}

func (r *CartItemRepository) Clear(ctx context.Context, cartID string) error {
	const query = `DELETE FROM cart_items WHERE cart_id = $1`
	_, err := r.pool.Exec(ctx, query, cartID)
	return err
}
