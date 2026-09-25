package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/order/internal/domain"
)

type VendorOrderRepository struct {
	pool *pgxpool.Pool
}

func NewVendorOrderRepository(pool *pgxpool.Pool) *VendorOrderRepository {
	return &VendorOrderRepository{pool: pool}
}

var ErrVendorOrderNotFound = errors.New("repository: vendor order not found")

const vendorOrderColumns = `id, order_id, vendor_id, status, subtotal_amount, shipping_fee_amount, currency, commission_rate_bps, commission_amount, net_amount, created_at, updated_at`

// scanner is satisfied by both pgx.Row (QueryRow) and pgx.Rows (Query), so
// the single-row and multi-row paths below share one scan implementation.
type scanner interface {
	Scan(dest ...any) error
}

func scanVendorOrder(row scanner) (*domain.VendorOrder, error) {
	var vo domain.VendorOrder
	err := row.Scan(
		&vo.ID, &vo.OrderID, &vo.VendorID, &vo.Status, &vo.SubtotalAmount, &vo.ShippingFeeAmount, &vo.Currency,
		&vo.CommissionRateBps, &vo.CommissionAmount, &vo.NetAmount, &vo.CreatedAt, &vo.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVendorOrderNotFound
		}
		return nil, err
	}
	return &vo, nil
}

func (r *VendorOrderRepository) FindByID(ctx context.Context, id string) (*domain.VendorOrder, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+vendorOrderColumns+` FROM vendor_orders WHERE id = $1`, id)
	return scanVendorOrder(row)
}

// ListByOrderID lets Order recompute a multi-vendor order's aggregate status
// from every one of its vendor sub-orders.
func (r *VendorOrderRepository) ListByOrderID(ctx context.Context, orderID string) ([]*domain.VendorOrder, error) {
	return r.list(ctx, `SELECT `+vendorOrderColumns+` FROM vendor_orders WHERE order_id = $1`, orderID)
}

// ListByVendor lets a vendor see their own sub-orders across every buyer
// order, without ever exposing another vendor's slice of the same order.
func (r *VendorOrderRepository) ListByVendor(ctx context.Context, vendorID string, limit, offset int) ([]*domain.VendorOrder, error) {
	return r.list(ctx, `SELECT `+vendorOrderColumns+` FROM vendor_orders WHERE vendor_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, vendorID, limit, offset)
}

func (r *VendorOrderRepository) list(ctx context.Context, query string, args ...any) ([]*domain.VendorOrder, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var vendorOrders []*domain.VendorOrder
	for rows.Next() {
		vo, err := scanVendorOrder(rows)
		if err != nil {
			return nil, err
		}
		vendorOrders = append(vendorOrders, vo)
	}
	return vendorOrders, rows.Err()
}

func (r *VendorOrderRepository) UpdateStatus(ctx context.Context, id string, status domain.Status) error {
	tag, err := r.pool.Exec(ctx, `UPDATE vendor_orders SET status = $1, updated_at = now() WHERE id = $2`, status, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVendorOrderNotFound
	}
	return nil
}

// SetCommission snapshots the commission split onto a vendor order once,
// when it's marked paid. It is not meant to be called again for the same
// row — a later commission rule change never touches an already-snapshotted
// vendor order.
func (r *VendorOrderRepository) SetCommission(ctx context.Context, id string, rateBps int, commissionAmount, netAmount int64) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE vendor_orders SET commission_rate_bps = $1, commission_amount = $2, net_amount = $3, updated_at = now() WHERE id = $4`,
		rateBps, commissionAmount, netAmount, id,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVendorOrderNotFound
	}
	return nil
}

// SetShippingFee is a follow-up snapshot, same shape as SetCommission:
// called once, right after Shipment has quoted a vendor sub-order's fee at
// checkout time — never touched again afterward, even if a later fee-rule
// edit would compute a different number for the same route.
func (r *VendorOrderRepository) SetShippingFee(ctx context.Context, id string, feeAmount int64) error {
	tag, err := r.pool.Exec(ctx, `UPDATE vendor_orders SET shipping_fee_amount = $1, updated_at = now() WHERE id = $2`, feeAmount, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVendorOrderNotFound
	}
	return nil
}

// paidOrFurtherStatuses are the vendor-order statuses that represent a
// completed sale for revenue-reporting purposes — everything from the
// moment payment succeeded onward, refunded included (the sale happened;
// the refund is a separate operational fact, not something that erases it
// from a vendor's historical revenue in this MVP).
var paidOrFurtherStatuses = []string{"paid", "processing", "shipped", "completed", "refunded"}

func (r *VendorOrderRepository) SummaryByVendor(ctx context.Context, vendorID string) (*domain.VendorSummary, error) {
	const query = `
		SELECT count(*), COALESCE(sum(subtotal_amount), 0), COALESCE(sum(commission_amount), 0), COALESCE(sum(net_amount), 0)
		FROM vendor_orders WHERE vendor_id = $1 AND status = ANY($2)`

	var s domain.VendorSummary
	err := r.pool.QueryRow(ctx, query, vendorID, paidOrFurtherStatuses).
		Scan(&s.TotalOrders, &s.TotalRevenue, &s.TotalCommission, &s.TotalNet)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *VendorOrderRepository) TopProductsByVendor(ctx context.Context, vendorID string, limit int) ([]*domain.TopProduct, error) {
	const query = `
		SELECT oi.product_id, oi.product_name, sum(oi.quantity), sum(oi.subtotal_amount)
		FROM order_items oi
		JOIN vendor_orders vo ON vo.id = oi.vendor_order_id
		WHERE vo.vendor_id = $1 AND vo.status = ANY($2)
		GROUP BY oi.product_id, oi.product_name
		ORDER BY sum(oi.quantity) DESC
		LIMIT $3`

	rows, err := r.pool.Query(ctx, query, vendorID, paidOrFurtherStatuses, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.TopProduct
	for rows.Next() {
		var p domain.TopProduct
		if err := rows.Scan(&p.ProductID, &p.ProductName, &p.QuantitySold, &p.RevenueAmount); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// ListItemsByVendorOrderIDs batch-looks-up order items scoped to several
// vendor orders at once, keyed by vendor_order_id — backs the vendor's own
// order list/export, which previously showed no item detail at all.
func (r *VendorOrderRepository) ListItemsByVendorOrderIDs(ctx context.Context, vendorOrderIDs []string) (map[string][]*domain.OrderItem, error) {
	if len(vendorOrderIDs) == 0 {
		return map[string][]*domain.OrderItem{}, nil
	}

	rows, err := r.pool.Query(ctx, `SELECT `+orderItemColumns+` FROM order_items WHERE vendor_order_id = ANY($1) ORDER BY created_at ASC`, vendorOrderIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]*domain.OrderItem, len(vendorOrderIDs))
	for rows.Next() {
		item, err := scanOrderItem(rows)
		if err != nil {
			return nil, err
		}
		out[item.VendorOrderID] = append(out[item.VendorOrderID], item)
	}
	return out, rows.Err()
}

// QuantitySoldByProductIDs aggregates units sold per product, across every
// vendor, for the public storefront listing — same "sold" definition as
// TopProductsByVendor above (paidOrFurtherStatuses, refunded included).
func (r *VendorOrderRepository) QuantitySoldByProductIDs(ctx context.Context, productIDs []string) (map[string]int64, error) {
	const query = `
		SELECT oi.product_id, sum(oi.quantity)
		FROM order_items oi
		JOIN vendor_orders vo ON vo.id = oi.vendor_order_id
		WHERE oi.product_id = ANY($1) AND vo.status = ANY($2)
		GROUP BY oi.product_id`

	rows, err := r.pool.Query(ctx, query, productIDs, paidOrFurtherStatuses)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]int64, len(productIDs))
	for rows.Next() {
		var productID string
		var quantity int64
		if err := rows.Scan(&productID, &quantity); err != nil {
			return nil, err
		}
		out[productID] = quantity
	}
	return out, rows.Err()
}
