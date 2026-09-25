package repository

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/order/internal/domain"
)

type OrderRepository struct {
	pool *pgxpool.Pool
}

func NewOrderRepository(pool *pgxpool.Pool) *OrderRepository {
	return &OrderRepository{pool: pool}
}

var ErrOrderNotFound = errors.New("repository: order not found")

const orderColumns = `id, buyer_id, status, total_amount, currency, cancellation_reason,
	recipient_name, phone, province, district, ward, street_address, created_at, updated_at`

func scanOrder(row pgx.Row) (*domain.Order, error) {
	var o domain.Order
	err := row.Scan(
		&o.ID, &o.BuyerID, &o.Status, &o.TotalAmount, &o.Currency, &o.CancellationReason,
		&o.RecipientName, &o.Phone, &o.Province, &o.District, &o.Ward, &o.StreetAddress,
		&o.CreatedAt, &o.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, err
	}
	return &o, nil
}

// CreateFromPlan persists an order, its vendor sub-orders and every order
// item in one transaction: a checkout either produces a complete order or
// none at all.
func (r *OrderRepository) CreateFromPlan(ctx context.Context, plan *domain.Plan) (*domain.Order, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	order := plan.Order
	err = tx.QueryRow(ctx,
		`INSERT INTO orders (buyer_id, status, total_amount, currency, recipient_name, phone, province, district, ward, street_address)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING id, created_at, updated_at`,
		order.BuyerID, order.Status, order.TotalAmount, order.Currency,
		order.RecipientName, order.Phone, order.Province, order.District, order.Ward, order.StreetAddress,
	).Scan(&order.ID, &order.CreatedAt, &order.UpdatedAt)
	if err != nil {
		return nil, err
	}

	vendorOrderIDs := make([]string, len(plan.VendorOrders))
	for i, vo := range plan.VendorOrders {
		err = tx.QueryRow(ctx,
			`INSERT INTO vendor_orders (order_id, vendor_id, status, subtotal_amount, currency) VALUES ($1, $2, $3, $4, $5)
			 RETURNING id`,
			order.ID, vo.VendorID, vo.Status, vo.SubtotalAmount, vo.Currency,
		).Scan(&vendorOrderIDs[i])
		if err != nil {
			return nil, err
		}
	}

	for _, item := range plan.Items {
		vendorIdx, err := strconv.Atoi(item.VendorOrderID)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO order_items (order_id, vendor_order_id, product_id, product_name, variant_id, variant_sku, variant_label, price_amount, quantity, subtotal_amount)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			order.ID, vendorOrderIDs[vendorIdx], item.ProductID, item.ProductName,
			item.VariantID, item.VariantSKU, item.VariantLabel,
			item.PriceAmount, item.Quantity, item.SubtotalAmount,
		); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &order, nil
}

func (r *OrderRepository) FindByID(ctx context.Context, id string) (*domain.Order, error) {
	return scanOrder(r.pool.QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE id = $1`, id))
}

func (r *OrderRepository) ListByBuyer(ctx context.Context, buyerID string, limit, offset int) ([]*domain.Order, error) {
	return r.list(ctx, `SELECT `+orderColumns+` FROM orders WHERE buyer_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, buyerID, limit, offset)
}

// ListByStatus serves the admin order view. An empty status lists every
// order regardless of status.
func (r *OrderRepository) ListByStatus(ctx context.Context, status string, limit, offset int) ([]*domain.Order, error) {
	return r.list(ctx, `SELECT `+orderColumns+` FROM orders WHERE ($1 = '' OR status = $1) ORDER BY created_at DESC LIMIT $2 OFFSET $3`, status, limit, offset)
}

func (r *OrderRepository) list(ctx context.Context, query string, args ...any) ([]*domain.Order, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []*domain.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

func (r *OrderRepository) UpdateStatus(ctx context.Context, id string, status domain.Status, reason *string) error {
	const query = `UPDATE orders SET status = $1, cancellation_reason = $2, updated_at = now() WHERE id = $3`
	tag, err := r.pool.Exec(ctx, query, status, reason, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrOrderNotFound
	}
	return nil
}

// UpdateTotalAmount is a follow-up update, called once right after
// checkout, once Shipment has quoted every vendor sub-order's fee — the
// order's total wasn't fully known at CreateFromPlan time, since the fee
// depends on a shipment that's created immediately afterward.
func (r *OrderRepository) UpdateTotalAmount(ctx context.Context, id string, totalAmount int64) error {
	const query = `UPDATE orders SET total_amount = $1, updated_at = now() WHERE id = $2`
	tag, err := r.pool.Exec(ctx, query, totalAmount, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrOrderNotFound
	}
	return nil
}

const orderItemColumns = `id, order_id, vendor_order_id, product_id, product_name, variant_id, variant_sku, variant_label, price_amount, quantity, subtotal_amount, created_at`

func scanOrderItem(row scanner) (*domain.OrderItem, error) {
	var item domain.OrderItem
	err := row.Scan(
		&item.ID, &item.OrderID, &item.VendorOrderID, &item.ProductID, &item.ProductName,
		&item.VariantID, &item.VariantSKU, &item.VariantLabel,
		&item.PriceAmount, &item.Quantity, &item.SubtotalAmount, &item.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *OrderRepository) ListItemsByOrder(ctx context.Context, orderID string) ([]*domain.OrderItem, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+orderItemColumns+` FROM order_items WHERE order_id = $1 ORDER BY created_at ASC`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*domain.OrderItem
	for rows.Next() {
		item, err := scanOrderItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
