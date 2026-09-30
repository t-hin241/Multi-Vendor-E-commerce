package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/cart/internal/domain"
)

// CartItemRepository reads and writes cart lines. Mutations assume the
// caller holds the cart row lock (CartRepository.LockForUser); the partial
// unique indexes stay as the last line of defense.
type CartItemRepository struct {
	pool *pgxpool.Pool
}

func NewCartItemRepository(pool *pgxpool.Pool) *CartItemRepository {
	return &CartItemRepository{pool: pool}
}

const itemColumns = `id, cart_id, product_id, variant_id, quantity, version, seen_price_amount, seen_currency, created_at, updated_at`

func scanItem(row pgx.Row) (*domain.CartItem, error) {
	var item domain.CartItem
	if err := row.Scan(&item.ID, &item.CartID, &item.ProductID, &item.VariantID, &item.Quantity, &item.Version,
		&item.SeenPriceAmount, &item.SeenCurrency, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *CartItemRepository) list(ctx context.Context, query string, args ...any) ([]*domain.CartItem, error) {
	ctx, cancel := statementContext(ctx)
	defer cancel()
	rows, err := connection(ctx, r.pool).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*domain.CartItem
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListByCart returns every line in a stable order (oldest first, id as tie
// break) so pagination over the result is deterministic.
func (r *CartItemRepository) ListByCart(ctx context.Context, cartID string) ([]*domain.CartItem, error) {
	return r.list(ctx, `SELECT `+itemColumns+` FROM cart_items WHERE cart_id = $1 ORDER BY created_at ASC, id ASC`, cartID)
}

// ListByIDs returns the named lines that still belong to cartID.
func (r *CartItemRepository) ListByIDs(ctx context.Context, cartID string, ids []string) ([]*domain.CartItem, error) {
	return r.list(ctx, `SELECT `+itemColumns+` FROM cart_items WHERE cart_id = $1 AND id = ANY($2::uuid[]) ORDER BY created_at ASC, id ASC`, cartID, ids)
}

// FindLine returns the line for productID/variantID, or nil if the cart has
// none.
func (r *CartItemRepository) FindLine(ctx context.Context, cartID, productID string, variantID *string) (*domain.CartItem, error) {
	ctx, cancel := statementContext(ctx)
	defer cancel()
	db := connection(ctx, r.pool)
	var row pgx.Row
	if variantID != nil {
		row = db.QueryRow(ctx, `SELECT `+itemColumns+` FROM cart_items WHERE cart_id = $1 AND variant_id = $2`, cartID, *variantID)
	} else {
		row = db.QueryRow(ctx, `SELECT `+itemColumns+` FROM cart_items WHERE cart_id = $1 AND product_id = $2 AND variant_id IS NULL`, cartID, productID)
	}
	item, err := scanItem(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return item, err
}

func (r *CartItemRepository) CountLines(ctx context.Context, cartID string) (int, error) {
	ctx, cancel := statementContext(ctx)
	defer cancel()
	var n int
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT count(*) FROM cart_items WHERE cart_id = $1`, cartID).Scan(&n)
	return n, err
}

// HasOtherCurrency reports whether any line remembers a price in a currency
// other than currency — the cheap guard against building a mixed-currency
// cart without a Catalog round trip per existing line. exceptLineID skips
// the line being updated itself.
func (r *CartItemRepository) HasOtherCurrency(ctx context.Context, cartID, currency string, exceptLineID *string) (bool, error) {
	ctx, cancel := statementContext(ctx)
	defer cancel()
	var exists bool
	err := connection(ctx, r.pool).QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM cart_items WHERE cart_id = $1 AND seen_currency IS NOT NULL AND seen_currency <> $2 AND id IS DISTINCT FROM $3::uuid)`,
		cartID, currency, exceptLineID).Scan(&exists)
	return exists, err
}

func (r *CartItemRepository) Insert(ctx context.Context, item *domain.CartItem) error {
	ctx, cancel := statementContext(ctx)
	defer cancel()
	return connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO cart_items (cart_id, product_id, variant_id, quantity, seen_price_amount, seen_currency)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, version, created_at, updated_at`,
		item.CartID, item.ProductID, item.VariantID, item.Quantity, item.SeenPriceAmount, item.SeenCurrency,
	).Scan(&item.ID, &item.Version, &item.CreatedAt, &item.UpdatedAt)
}

// Update writes quantity and the price reference of an existing line and
// bumps its version.
func (r *CartItemRepository) Update(ctx context.Context, item *domain.CartItem) error {
	ctx, cancel := statementContext(ctx)
	defer cancel()
	return connection(ctx, r.pool).QueryRow(ctx, `
		UPDATE cart_items
		SET quantity = $2, seen_price_amount = $3, seen_currency = $4, version = version + 1, updated_at = now()
		WHERE id = $1
		RETURNING version, updated_at`,
		item.ID, item.Quantity, item.SeenPriceAmount, item.SeenCurrency,
	).Scan(&item.Version, &item.UpdatedAt)
}

func (r *CartItemRepository) Delete(ctx context.Context, cartID, lineID string) (bool, error) {
	ctx, cancel := statementContext(ctx)
	defer cancel()
	tag, err := connection(ctx, r.pool).Exec(ctx, `DELETE FROM cart_items WHERE cart_id = $1 AND id = $2`, cartID, lineID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *CartItemRepository) DeleteAll(ctx context.Context, cartID string) (int64, error) {
	ctx, cancel := statementContext(ctx)
	defer cancel()
	tag, err := connection(ctx, r.pool).Exec(ctx, `DELETE FROM cart_items WHERE cart_id = $1`, cartID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
