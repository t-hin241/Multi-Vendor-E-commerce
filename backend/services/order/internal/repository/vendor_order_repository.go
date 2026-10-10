package repository

import (
	"context"
	"errors"
	"time"

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

const vendorOrderColumns = `id, order_id, vendor_id, status, version, subtotal_amount, shipping_fee_amount, refunded_amount, currency,
	shipping_carrier_id, shipping_zone_id, shipping_fee_rule_id, shipping_fee_rule_version, package_weight_grams, shipping_quoted_at,
	commission_rule_id, commission_rule_version, commission_rate_bps, commission_base_amount, commission_amount, net_amount,
	commission_rounding, commission_source, completed_at, created_at, updated_at`

// scanner is satisfied by both pgx.Row (QueryRow) and pgx.Rows (Query), so
// the single-row and multi-row paths below share one scan implementation.
type scanner interface {
	Scan(dest ...any) error
}

func scanVendorOrder(row scanner) (*domain.VendorOrder, error) {
	var (
		vo                             domain.VendorOrder
		carrierID, zoneID, feeRuleID   *string
		feeRuleVersion                 *int
		weight                         *int64
		quotedAt                       *time.Time
		ruleID                         *string
		ruleVersion, base, amount, net *int64
		rate                           *int
		rounding, source               *string
	)
	err := row.Scan(
		&vo.ID, &vo.OrderID, &vo.VendorID, &vo.Status, &vo.Version, &vo.SubtotalAmount, &vo.ShippingFeeAmount, &vo.RefundedAmount, &vo.Currency,
		&carrierID, &zoneID, &feeRuleID, &feeRuleVersion, &weight, &quotedAt,
		&ruleID, &ruleVersion, &rate, &base, &amount, &net, &rounding, &source,
		&vo.CompletedAt, &vo.CreatedAt, &vo.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVendorOrderNotFound
		}
		return nil, err
	}
	if feeRuleID != nil {
		q := domain.ShippingQuote{VendorID: vo.VendorID, FeeAmount: vo.ShippingFeeAmount, Currency: vo.Currency, FeeRuleID: *feeRuleID}
		if carrierID != nil {
			q.CarrierID = *carrierID
		}
		if zoneID != nil {
			q.ZoneID = *zoneID
		}
		if feeRuleVersion != nil {
			q.FeeRuleVersion = *feeRuleVersion
		}
		if weight != nil {
			q.PackageWeightGrams = *weight
		}
		if quotedAt != nil {
			q.QuotedAt = *quotedAt
		}
		vo.Shipping = &q
	}
	vo.CommissionRateBps, vo.CommissionAmount, vo.NetAmount = rate, amount, net
	if rate != nil && amount != nil && net != nil {
		c := &domain.CommissionSnapshot{RuleID: ruleID, RuleVersion: ruleVersion, RateBps: *rate, Amount: *amount, NetAmount: *net}
		if base != nil {
			c.BaseAmount = *base
		}
		if rounding != nil {
			c.Rounding = *rounding
		}
		if source != nil {
			c.Source = *source
		}
		vo.Commission = c
	}
	return &vo, nil
}

func (r *VendorOrderRepository) FindByID(ctx context.Context, id string) (*domain.VendorOrder, error) {
	return scanVendorOrder(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+vendorOrderColumns+` FROM vendor_orders WHERE id = $1`, id))
}

// ListByOrderID lists every vendor sub-order of an order.
func (r *VendorOrderRepository) ListByOrderID(ctx context.Context, orderID string) ([]*domain.VendorOrder, error) {
	return r.list(ctx, `SELECT `+vendorOrderColumns+` FROM vendor_orders WHERE order_id = $1 ORDER BY created_at, id`, orderID)
}

// ListByVendor lets a vendor see their own sub-orders across every buyer
// order, optionally filtered by status, without ever exposing another
// vendor's slice of the same order.
func (r *VendorOrderRepository) ListByVendor(ctx context.Context, vendorID, status string, limit, offset int) ([]*domain.VendorOrder, error) {
	return r.list(ctx, `SELECT `+vendorOrderColumns+` FROM vendor_orders WHERE vendor_id = $1 AND ($2 = '' OR status = $2)
		ORDER BY created_at DESC, id DESC LIMIT $3 OFFSET $4`, vendorID, status, limit, offset)
}

func (r *VendorOrderRepository) list(ctx context.Context, query string, args ...any) ([]*domain.VendorOrder, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	vendorOrders := []*domain.VendorOrder{}
	for rows.Next() {
		vo, err := scanVendorOrder(rows)
		if err != nil {
			return nil, err
		}
		vendorOrders = append(vendorOrders, vo)
	}
	return vendorOrders, rows.Err()
}

// TransitionStatus moves one vendor order from one status to another only
// if it is still in from (compare-and-set).
func (r *VendorOrderRepository) TransitionStatus(ctx context.Context, id string, from, to domain.Status) error {
	tag, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE vendor_orders SET status = $3, version = version + 1,
		    completed_at = CASE WHEN $3 = 'completed' THEN now() ELSE completed_at END, updated_at = now()
		WHERE id = $1 AND status = $2`, id, from, to)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleState
	}
	return nil
}

// TransitionAllForOrder moves every vendor order of an order that is still
// in from to to, in the caller's transaction, and returns how many moved.
func (r *VendorOrderRepository) TransitionAllForOrder(ctx context.Context, orderID string, from, to domain.Status) (int64, error) {
	tag, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE vendor_orders SET status = $3, version = version + 1, updated_at = now()
		WHERE order_id = $1 AND status = $2`, orderID, from, to)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// SetLegacyCommission snapshots commission at payment time for an order
// created before commission moved to checkout. Never overwrites a snapshot.
func (r *VendorOrderRepository) SetLegacyCommission(ctx context.Context, id string, rule *domain.CommissionRule, commissionAmount, netAmount, base int64) error {
	_, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE vendor_orders SET commission_rule_id = $2, commission_rule_version = $3, commission_rate_bps = $4,
		    commission_base_amount = $5, commission_amount = $6, net_amount = $7, commission_rounding = 'floor',
		    commission_source = 'payment_time_legacy', updated_at = now()
		WHERE id = $1 AND commission_rate_bps IS NULL`,
		id, rule.ID, rule.Version, rule.RateBps, base, commissionAmount, netAmount)
	return err
}

// AddRefunded records money Payment confirmed as returned for a vendor
// order and returns the new refunded total.
func (r *VendorOrderRepository) AddRefunded(ctx context.Context, id string, amount int64) (int64, error) {
	var refunded int64
	err := connection(ctx, r.pool).QueryRow(ctx, `
		UPDATE vendor_orders SET refunded_amount = refunded_amount + $2, version = version + 1, updated_at = now()
		WHERE id = $1 RETURNING refunded_amount`, id, amount).Scan(&refunded)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrVendorOrderNotFound
	}
	return refunded, err
}

// paidOrFurtherStatuses are the vendor-order statuses that represent a
// confirmed sale: everything from a verified capture onward. Refunds are
// reported separately from the refunded amounts Payment confirmed.
var paidOrFurtherStatuses = []string{"paid", "processing", "shipped", "completed", "refunded"}

// SummaryByVendor aggregates only confirmed sales and confirmed refunds, so
// the vendor dashboard never counts an unpaid order or an unconfirmed
// refund.
func (r *VendorOrderRepository) SummaryByVendor(ctx context.Context, vendorID string) (*domain.VendorSummary, error) {
	const query = `
		SELECT count(*), COALESCE(sum(subtotal_amount), 0), COALESCE(sum(commission_amount), 0), COALESCE(sum(net_amount), 0),
		       COALESCE(sum(refunded_amount), 0)
		FROM vendor_orders WHERE vendor_id = $1 AND status = ANY($2)`

	var s domain.VendorSummary
	err := connection(ctx, r.pool).QueryRow(ctx, query, vendorID, paidOrFurtherStatuses).
		Scan(&s.TotalOrders, &s.TotalRevenue, &s.TotalCommission, &s.TotalNet, &s.TotalRefunded)
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
		ORDER BY sum(oi.quantity) DESC, oi.product_id
		LIMIT $3`

	rows, err := connection(ctx, r.pool).Query(ctx, query, vendorID, paidOrFurtherStatuses, limit)
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
// vendor orders at once, keyed by vendor_order_id.
func (r *VendorOrderRepository) ListItemsByVendorOrderIDs(ctx context.Context, vendorOrderIDs []string) (map[string][]*domain.OrderItem, error) {
	if len(vendorOrderIDs) == 0 {
		return map[string][]*domain.OrderItem{}, nil
	}

	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT `+orderItemColumns+` FROM order_items WHERE vendor_order_id = ANY($1) ORDER BY created_at ASC, id ASC`, vendorOrderIDs)
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
// vendor, for the public storefront listing.
func (r *VendorOrderRepository) QuantitySoldByProductIDs(ctx context.Context, productIDs []string) (map[string]int64, error) {
	const query = `
		SELECT oi.product_id, sum(oi.quantity)
		FROM order_items oi
		JOIN vendor_orders vo ON vo.id = oi.vendor_order_id
		WHERE oi.product_id = ANY($1) AND vo.status = ANY($2)
		GROUP BY oi.product_id`

	rows, err := connection(ctx, r.pool).Query(ctx, query, productIDs, paidOrFurtherStatuses)
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

// HeldForSettlement names which of ids have a return request, a refund or
// a support case that may affect money still open; Payment must not pay
// those vendor orders out yet. A support case holds until it is closed
// (a resolved case can still be reopened).
func (r *VendorOrderRepository) HeldForSettlement(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := connection(ctx, r.pool).Query(ctx, `
		SELECT oi.vendor_order_id::text, 'return_open' FROM return_requests rr JOIN order_items oi ON oi.id = rr.order_item_id
		WHERE oi.vendor_order_id::text = ANY($1) AND rr.status NOT IN ('rejected', 'refunded')
		UNION ALL
		SELECT vendor_order_id::text, 'refund_open' FROM order_refunds
		WHERE vendor_order_id::text = ANY($1) AND status IN ('requested', 'submitted')
		UNION ALL
		SELECT vendor_order_id::text, 'support_case_open' FROM support_cases
		WHERE vendor_order_id::text = ANY($1) AND financial_hold AND status <> 'closed'
		UNION ALL
		SELECT vendor_order_id::text, 'cancellation_open' FROM cancellation_requests
		WHERE vendor_order_id::text = ANY($1) AND status NOT IN ('rejected', 'resolved', 'transferred')
		UNION ALL
		SELECT vendor_order_id::text, 'delivery_exception_open' FROM delivery_exceptions
		WHERE vendor_order_id::text = ANY($1) AND status <> 'resolved'`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, reason string
		if err := rows.Scan(&id, &reason); err != nil {
			return nil, err
		}
		if _, seen := out[id]; !seen {
			out[id] = reason
		}
	}
	return out, rows.Err()
}
