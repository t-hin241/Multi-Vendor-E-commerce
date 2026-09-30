package repository

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/productsales"
	"shopee/backend/pkg/vendorsales"
	"shopee/backend/services/order/internal/domain"
)

type OrderRepository struct {
	pool *pgxpool.Pool
}

func NewOrderRepository(pool *pgxpool.Pool) *OrderRepository {
	return &OrderRepository{pool: pool}
}

var ErrOrderNotFound = errors.New("repository: order not found")

// ErrStaleState means a compare-and-set transition found the row in a
// different state than expected: someone else changed it first.
var ErrStaleState = errors.New("repository: row changed concurrently")

const orderColumns = `id, buyer_id, status, version, checkout_state, subtotal_amount, shipping_amount, total_amount, refunded_amount,
	currency, cancellation_reason, recipient_name, phone, province, district, ward, street_address, paid_at, created_at, updated_at`

func scanOrder(row pgx.Row) (*domain.Order, error) {
	var o domain.Order
	err := row.Scan(
		&o.ID, &o.BuyerID, &o.Status, &o.Version, &o.CheckoutState, &o.SubtotalAmount, &o.ShippingAmount, &o.TotalAmount, &o.RefundedAmount,
		&o.Currency, &o.CancellationReason, &o.RecipientName, &o.Phone, &o.Province, &o.District, &o.Ward, &o.StreetAddress,
		&o.PaidAt, &o.CreatedAt, &o.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, err
	}
	return &o, nil
}

// CreateFromPlan persists an order, its vendor sub-orders (with their
// shipping and commission snapshots), every order item, the held cart
// consume task and the link to the checkout operation in one transaction:
// a checkout either produces a complete order or none at all.
func (r *OrderRepository) CreateFromPlan(ctx context.Context, plan *domain.Plan) (created *domain.Order, err error) {
	for _, vo := range plan.VendorOrders {
		if plan.VendorVersions[vo.VendorID] < 1 {
			return nil, apperror.Conflict("Missing live shop permission")
		}
	}
	for _, item := range plan.Items {
		if plan.ProductVersions[item.ProductID] < 1 {
			return nil, apperror.Conflict("Product version unavailable; retry checkout")
		}
	}
	err = (Transactions{Pool: r.pool}).Run(ctx, func(ctx context.Context) error {
		tx := ctx.Value(transactionKey{}).(pgx.Tx)
		if err := vendorsales.LockApproved(ctx, tx, plan.VendorVersions); err != nil {
			return err
		}
		if err := productsales.LockVisible(ctx, tx, plan.ProductVersions); err != nil {
			return err
		}

		order := plan.Order
		if order.CheckoutState == "" {
			order.CheckoutState = domain.CheckoutReady
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO orders (buyer_id, status, checkout_state, subtotal_amount, shipping_amount, total_amount, currency,
			                    recipient_name, phone, province, district, ward, street_address)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			RETURNING id, version, created_at, updated_at`,
			order.BuyerID, order.Status, order.CheckoutState, order.SubtotalAmount, order.ShippingAmount, order.TotalAmount, order.Currency,
			order.RecipientName, order.Phone, order.Province, order.District, order.Ward, order.StreetAddress,
		).Scan(&order.ID, &order.Version, &order.CreatedAt, &order.UpdatedAt); err != nil {
			return err
		}

		vendorOrderIDs := make([]string, len(plan.VendorOrders))
		for i, vo := range plan.VendorOrders {
			var q domain.ShippingQuote
			if vo.Shipping != nil {
				q = *vo.Shipping
			}
			c := vo.Commission
			if c == nil {
				c = &domain.CommissionSnapshot{}
			}
			if err := tx.QueryRow(ctx, `
				INSERT INTO vendor_orders (order_id, vendor_id, status, subtotal_amount, shipping_fee_amount, currency,
				    shipping_carrier_id, shipping_zone_id, shipping_fee_rule_id, shipping_fee_rule_version, package_weight_grams, shipping_quoted_at,
				    commission_rule_id, commission_rule_version, commission_rate_bps, commission_base_amount, commission_amount, net_amount,
				    commission_rounding, commission_source)
				VALUES ($1, $2, $3, $4, $5, $6,
				    NULLIF($7, '')::uuid, NULLIF($8, '')::uuid, NULLIF($9, '')::uuid, NULLIF($10, 0), $11, $12,
				    $13, $14, $15, $16, $17, $18, NULLIF($19, ''), NULLIF($20, ''))
				RETURNING id`,
				order.ID, vo.VendorID, vo.Status, vo.SubtotalAmount, vo.ShippingFeeAmount, vo.Currency,
				q.CarrierID, q.ZoneID, q.FeeRuleID, q.FeeRuleVersion, nullableWeight(vo.Shipping), nullableTime(vo.Shipping),
				c.RuleID, c.RuleVersion, nullableRate(vo.Commission), nullableBase(vo.Commission), nullableAmount(vo.Commission, false), nullableAmount(vo.Commission, true),
				c.Rounding, c.Source,
			).Scan(&vendorOrderIDs[i]); err != nil {
				return err
			}
		}

		for _, item := range plan.Items {
			vendorIdx, err := strconv.Atoi(item.VendorOrderID)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO order_items (order_id, vendor_order_id, product_id, product_name, variant_id, variant_sku, variant_label, price_amount, quantity, subtotal_amount)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				order.ID, vendorOrderIDs[vendorIdx], item.ProductID, item.ProductName,
				item.VariantID, item.VariantSKU, item.VariantLabel,
				item.PriceAmount, item.Quantity, item.SubtotalAmount,
			); err != nil {
				return err
			}
		}

		if plan.CartConsumption != nil {
			if err := insertCartConsumption(ctx, tx, order.ID, plan.CartConsumption); err != nil {
				return err
			}
		}
		if plan.CheckoutOperationID != "" {
			tag, err := tx.Exec(ctx, `UPDATE checkout_operations SET order_id = $2, updated_at = now() WHERE id = $1 AND status = 'preparing' AND order_id IS NULL`,
				plan.CheckoutOperationID, order.ID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return apperror.Conflict("Checkout operation is no longer open")
			}
		}
		created = &order
		return nil
	})
	return created, err
}

func nullableWeight(q *domain.ShippingQuote) *int64 {
	if q == nil {
		return nil
	}
	return &q.PackageWeightGrams
}

func nullableTime(q *domain.ShippingQuote) *time.Time {
	if q == nil || q.QuotedAt.IsZero() {
		return nil
	}
	return &q.QuotedAt
}

func nullableRate(c *domain.CommissionSnapshot) *int {
	if c == nil {
		return nil
	}
	return &c.RateBps
}

func nullableBase(c *domain.CommissionSnapshot) *int64 {
	if c == nil {
		return nil
	}
	return &c.BaseAmount
}

func nullableAmount(c *domain.CommissionSnapshot, net bool) *int64 {
	if c == nil {
		return nil
	}
	if net {
		return &c.NetAmount
	}
	return &c.Amount
}

func (r *OrderRepository) FindByID(ctx context.Context, id string) (*domain.Order, error) {
	return scanOrder(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE id = $1`, id))
}

// OrderFilter narrows an order listing. Empty fields do not filter.
type OrderFilter struct {
	BuyerID string
	Status  string
	From    *time.Time
	To      *time.Time
}

// List returns one page of orders (newest first, id as tie-break) and the
// total number of matching orders.
func (r *OrderRepository) List(ctx context.Context, f OrderFilter, limit, offset int) ([]*domain.Order, int64, error) {
	const where = `WHERE ($1 = '' OR buyer_id::text = $1) AND ($2 = '' OR status = $2)
		AND ($3::timestamptz IS NULL OR created_at >= $3) AND ($4::timestamptz IS NULL OR created_at < $4)`
	var total int64
	if err := connection(ctx, r.pool).QueryRow(ctx, `SELECT count(*) FROM orders `+where, f.BuyerID, f.Status, f.From, f.To).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT `+orderColumns+` FROM orders `+where+`
		ORDER BY created_at DESC, id DESC LIMIT $5 OFFSET $6`, f.BuyerID, f.Status, f.From, f.To, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	orders := []*domain.Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, 0, err
		}
		orders = append(orders, o)
	}
	return orders, total, rows.Err()
}

// TransitionStatus moves an order from one status to another only if it is
// still in from (compare-and-set), bumping its version. reason, when set,
// is stored as the cancellation/refund reason.
func (r *OrderRepository) TransitionStatus(ctx context.Context, id string, from, to domain.Status, reason *string) error {
	tag, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE orders SET status = $3, cancellation_reason = COALESCE($4, cancellation_reason), version = version + 1,
		    paid_at = CASE WHEN $3 = 'paid' THEN now() ELSE paid_at END, updated_at = now()
		WHERE id = $1 AND status = $2`, id, from, to, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleState
	}
	return nil
}

// SetCheckoutState finishes (ready) or abandons (failed) a preparing order.
func (r *OrderRepository) SetCheckoutState(ctx context.Context, id string, state domain.CheckoutState) error {
	tag, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE orders SET checkout_state = $2, version = version + 1, updated_at = now()
		WHERE id = $1 AND checkout_state = 'preparing'`, id, state)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleState
	}
	return nil
}

// AddRefunded records money Payment confirmed as returned for the order.
func (r *OrderRepository) AddRefunded(ctx context.Context, id string, amount int64) error {
	tag, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE orders SET refunded_amount = refunded_amount + $2, version = version + 1, updated_at = now() WHERE id = $1`, id, amount)
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
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT `+orderItemColumns+` FROM order_items WHERE order_id = $1 ORDER BY created_at ASC, id ASC`, orderID)
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

// FindItem returns one order item.
func (r *OrderRepository) FindItem(ctx context.Context, orderID, itemID string) (*domain.OrderItem, error) {
	item, err := scanOrderItem(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+orderItemColumns+` FROM order_items WHERE order_id = $1 AND id = $2`, orderID, itemID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOrderNotFound
	}
	return item, err
}

// ListReviewEligibility is an internal, purchase-proof-only projection for
// Review. The buyer can review only completed vendor sub-orders.
func (r *OrderRepository) ListReviewEligibility(ctx context.Context, buyerID, productID string) ([]*domain.ReviewEligibility, error) {
	const query = `
		SELECT oi.id, oi.vendor_order_id, oi.product_id, vo.vendor_id, oi.product_name, oi.variant_label, coalesce(vo.completed_at, vo.updated_at)
		FROM order_items oi
		JOIN orders o ON o.id = oi.order_id
		JOIN vendor_orders vo ON vo.id = oi.vendor_order_id
		WHERE o.buyer_id = $1 AND vo.status = 'completed' AND ($2 = '' OR oi.product_id::text = $2)
		ORDER BY vo.updated_at DESC`
	rows, err := connection(ctx, r.pool).Query(ctx, query, buyerID, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*domain.ReviewEligibility, 0)
	for rows.Next() {
		var item domain.ReviewEligibility
		if err := rows.Scan(&item.OrderItemID, &item.VendorOrderID, &item.ProductID, &item.VendorID, &item.ProductName, &item.VariantLabel, &item.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, &item)
	}
	return out, rows.Err()
}

// WithLockedOrder runs fn in a transaction holding the order row lock, so
// every transition of one order (payment, cancel, refund, fulfillment) is
// serialized.
func (r *OrderRepository) WithLockedOrder(ctx context.Context, id string, fn func(context.Context) error) error {
	return (Transactions{Pool: r.pool}).Run(ctx, func(ctx context.Context) error {
		var locked string
		err := connection(ctx, r.pool).QueryRow(ctx, `SELECT id FROM orders WHERE id=$1 FOR UPDATE`, id).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			return apperror.NotFound("Order not found")
		}
		if err != nil {
			return err
		}
		return fn(ctx)
	})
}
