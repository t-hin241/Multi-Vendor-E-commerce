package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/payment/internal/domain"
)

var ErrSettlementOrderNotFound = errors.New("repository: settlement vendor order not found")

const settlementOrderColumns = `vendor_order_id, order_id, vendor_id, currency, subtotal_amount, shipping_amount, commission_amount,
	commission_rate_bps, commission_rule_version, completed_at, eligible_at, policy_version`

const entryColumns = `id, vendor_id, vendor_order_id, entry_type, amount, currency, base_amount, source_ref, eligible_at, note, created_by, created_at`

// SettlementRepository reads and appends the settlement ledger. Rows are
// never updated or deleted (the database refuses it).
type SettlementRepository struct{ pool *pgxpool.Pool }

func NewSettlementRepository(pool *pgxpool.Pool) *SettlementRepository {
	return &SettlementRepository{pool: pool}
}

func scanSettlementOrder(row pgx.Row) (*domain.SettlementOrder, error) {
	var o domain.SettlementOrder
	err := row.Scan(&o.VendorOrderID, &o.OrderID, &o.VendorID, &o.Currency, &o.SubtotalAmount, &o.ShippingAmount, &o.CommissionAmount,
		&o.CommissionRateBps, &o.CommissionRuleVersion, &o.CompletedAt, &o.EligibleAt, &o.PolicyVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSettlementOrderNotFound
	}
	return &o, err
}

func scanEntry(row pgx.Row) (*domain.Entry, error) {
	var e domain.Entry
	err := row.Scan(&e.ID, &e.VendorID, &e.VendorOrderID, &e.Type, &e.Amount, &e.Currency, &e.BaseAmount, &e.SourceRef, &e.EligibleAt, &e.Note, &e.CreatedBy, &e.CreatedAt)
	return &e, err
}

func (r *SettlementRepository) entries(ctx context.Context, query string, args ...any) ([]*domain.Entry, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.Entry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// InsertOrder stores a vendor order snapshot once. It returns the stored
// snapshot and whether this call created it.
func (r *SettlementRepository) InsertOrder(ctx context.Context, o domain.SettlementOrder) (*domain.SettlementOrder, bool, error) {
	stored, err := scanSettlementOrder(connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO settlement_vendor_orders (vendor_order_id, order_id, vendor_id, currency, subtotal_amount, shipping_amount,
			commission_amount, commission_rate_bps, commission_rule_version, completed_at, eligible_at, policy_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (vendor_order_id) DO NOTHING
		RETURNING `+settlementOrderColumns,
		o.VendorOrderID, o.OrderID, o.VendorID, o.Currency, o.SubtotalAmount, o.ShippingAmount, o.CommissionAmount,
		o.CommissionRateBps, o.CommissionRuleVersion, o.CompletedAt, o.EligibleAt, o.PolicyVersion))
	if err == nil {
		return stored, true, nil
	}
	if !errors.Is(err, ErrSettlementOrderNotFound) {
		return nil, false, err
	}
	stored, err = r.FindOrder(ctx, o.VendorOrderID)
	return stored, false, err
}

func (r *SettlementRepository) FindOrder(ctx context.Context, vendorOrderID string) (*domain.SettlementOrder, error) {
	return scanSettlementOrder(connection(ctx, r.pool).QueryRow(ctx,
		`SELECT `+settlementOrderColumns+` FROM settlement_vendor_orders WHERE vendor_order_id = $1`, vendorOrderID))
}

// Append inserts entries; one already recorded for the same type and source
// is skipped, which makes every posting idempotent.
func (r *SettlementRepository) Append(ctx context.Context, entries []domain.Entry) error {
	for _, e := range entries {
		if _, err := connection(ctx, r.pool).Exec(ctx, `
			INSERT INTO settlement_entries (vendor_id, vendor_order_id, entry_type, amount, currency, base_amount, source_ref, eligible_at, note, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (entry_type, source_ref) DO NOTHING`,
			e.VendorID, e.VendorOrderID, e.Type, e.Amount, e.Currency, e.BaseAmount, e.SourceRef, e.EligibleAt, e.Note, e.CreatedBy); err != nil {
			return err
		}
	}
	return nil
}

// RefundAttribution sums what earlier refunds of a vendor order attributed
// to items and reversed in commission.
func (r *SettlementRepository) RefundAttribution(ctx context.Context, vendorOrderID string) (refundedItems, reversed int64, err error) {
	err = connection(ctx, r.pool).QueryRow(ctx, `SELECT
		COALESCE(SUM(base_amount) FILTER (WHERE entry_type = 'refund'), 0),
		COALESCE(SUM(amount) FILTER (WHERE entry_type = 'commission_reversal'), 0)
		FROM settlement_entries WHERE vendor_order_id = $1`, vendorOrderID).Scan(&refundedItems, &reversed)
	return refundedItems, reversed, err
}

// HasEntry reports whether an entry of this type and source exists.
func (r *SettlementRepository) HasEntry(ctx context.Context, t domain.EntryType, source string) (bool, error) {
	var ok bool
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM settlement_entries WHERE entry_type = $1 AND source_ref = $2)`, t, source).Scan(&ok)
	return ok, err
}

// SettledRefund is a succeeded refund charged to a vendor order.
type SettledRefund struct {
	ID         string
	Amount     int64
	ResolvedAt time.Time
}

// SucceededRefunds lists a vendor order's succeeded refunds, oldest first,
// so ones confirmed before the order was settled can be posted.
func (r *SettlementRepository) SucceededRefunds(ctx context.Context, vendorOrderID string) ([]SettledRefund, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `
		SELECT id, amount, COALESCE(resolved_at, updated_at) FROM payment_refunds
		WHERE vendor_order_id = $1 AND status = 'succeeded' ORDER BY COALESCE(resolved_at, updated_at), id`, vendorOrderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SettledRefund{}
	for rows.Next() {
		var s SettledRefund
		if err := rows.Scan(&s.ID, &s.Amount, &s.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// OpenRefundVendorOrders returns which of ids have a refund not yet resolved
// in Payment; their credits are held from payouts.
func (r *SettlementRepository) OpenRefundVendorOrders(ctx context.Context, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := connection(ctx, r.pool).Query(ctx, `
		SELECT DISTINCT vendor_order_id::text FROM payment_refunds
		WHERE vendor_order_id::text = ANY($1) AND status IN ('awaiting_provider_refund', 'pending')`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Unpaid lists a vendor's entries in currency that no pending or succeeded
// payout item includes yet (payout entries excluded).
func (r *SettlementRepository) Unpaid(ctx context.Context, vendorID, currency string) ([]*domain.Entry, error) {
	return r.entries(ctx, `SELECT `+entryColumns+` FROM settlement_entries e
		WHERE e.vendor_id = $1 AND e.currency = $2 AND e.entry_type <> 'payout'
		  AND NOT EXISTS (SELECT 1 FROM payout_item_entries l JOIN payout_items i ON i.id = l.payout_item_id
		                  WHERE l.entry_id = e.id AND i.status IN ('pending', 'succeeded'))
		ORDER BY e.created_at, e.id`, vendorID, currency)
}

// VendorsWithUnpaid lists vendors with unpaid entries in currency.
func (r *SettlementRepository) VendorsWithUnpaid(ctx context.Context, currency string) ([]string, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT DISTINCT e.vendor_id::text FROM settlement_entries e
		WHERE e.currency = $1 AND e.entry_type <> 'payout'
		  AND NOT EXISTS (SELECT 1 FROM payout_item_entries l JOIN payout_items i ON i.id = l.payout_item_id
		                  WHERE l.entry_id = e.id AND i.status IN ('pending', 'succeeded'))
		ORDER BY 1`, currency)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Statement lists a vendor's entries, newest first.
func (r *SettlementRepository) Statement(ctx context.Context, vendorID, currency string, limit, offset int) ([]*domain.Entry, error) {
	return r.entries(ctx, `SELECT `+entryColumns+` FROM settlement_entries WHERE vendor_id = $1 AND currency = $2
		ORDER BY created_at DESC, id DESC LIMIT $3 OFFSET $4`, vendorID, currency, limit, offset)
}

// VendorBalance summarises one vendor's ledger.
type VendorBalance struct {
	VendorID string `json:"vendor_id"`
	Currency string `json:"currency"`
	// Owed is the sum of every entry: what the marketplace still owes.
	Owed int64 `json:"owed"`
	// Eligible is unpaid entries past their return window (before holds).
	Eligible int64 `json:"eligible"`
	// InPayout is included in payout items not resolved yet.
	InPayout   int64 `json:"in_payout"`
	Sales      int64 `json:"sales"`
	Commission int64 `json:"commission"`
	Refunded   int64 `json:"refunded"`
	PaidOut    int64 `json:"paid_out"`
}

const balanceSelect = `SELECT e.vendor_id::text, e.currency,
	COALESCE(SUM(e.amount), 0),
	COALESCE(SUM(e.amount) FILTER (WHERE e.entry_type <> 'payout' AND u.entry_id IS NULL AND (e.entry_type = 'refund' OR (e.entry_type = 'adjustment' AND e.amount < 0) OR e.eligible_at <= now())), 0),
	COALESCE(SUM(e.amount) FILTER (WHERE p.entry_id IS NOT NULL), 0),
	COALESCE(SUM(e.amount) FILTER (WHERE e.entry_type IN ('sale', 'shipping')), 0),
	COALESCE(-SUM(e.amount) FILTER (WHERE e.entry_type IN ('commission', 'commission_reversal')), 0),
	COALESCE(-SUM(e.amount) FILTER (WHERE e.entry_type = 'refund'), 0),
	COALESCE(-SUM(e.amount) FILTER (WHERE e.entry_type = 'payout'), 0)
	FROM settlement_entries e
	LEFT JOIN (SELECT DISTINCT l.entry_id FROM payout_item_entries l JOIN payout_items i ON i.id = l.payout_item_id
	           WHERE i.status IN ('pending', 'succeeded')) u ON u.entry_id = e.id
	LEFT JOIN (SELECT DISTINCT l.entry_id FROM payout_item_entries l JOIN payout_items i ON i.id = l.payout_item_id
	           WHERE i.status = 'pending') p ON p.entry_id = e.id`

func scanBalances(rows pgx.Rows) ([]VendorBalance, error) {
	defer rows.Close()
	out := []VendorBalance{}
	for rows.Next() {
		var b VendorBalance
		if err := rows.Scan(&b.VendorID, &b.Currency, &b.Owed, &b.Eligible, &b.InPayout, &b.Sales, &b.Commission, &b.Refunded, &b.PaidOut); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Balances lists every vendor's balance in currency, largest owed first.
func (r *SettlementRepository) Balances(ctx context.Context, currency string, limit, offset int) ([]VendorBalance, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, balanceSelect+`
		WHERE e.currency = $1 GROUP BY e.vendor_id, e.currency ORDER BY 3 DESC, 1 LIMIT $2 OFFSET $3`, currency, limit, offset)
	if err != nil {
		return nil, err
	}
	return scanBalances(rows)
}

// Report sums one vendor's ledger entries created in [from, to).
func (r *SettlementRepository) Report(ctx context.Context, vendorID, currency string, from, to time.Time) (sales, refunded, paidOut, eligibleNow int64, err error) {
	err = connection(ctx, r.pool).QueryRow(ctx, `SELECT
		COALESCE(SUM(amount) FILTER (WHERE entry_type IN ('sale', 'shipping') AND created_at >= $3 AND created_at < $4), 0),
		COALESCE(-SUM(amount) FILTER (WHERE entry_type = 'refund' AND created_at >= $3 AND created_at < $4), 0),
		COALESCE(-SUM(amount) FILTER (WHERE entry_type = 'payout' AND created_at >= $3 AND created_at < $4), 0),
		COALESCE(SUM(amount) FILTER (WHERE entry_type <> 'payout' AND (entry_type = 'refund' OR (entry_type = 'adjustment' AND amount < 0) OR eligible_at <= now())
			AND NOT EXISTS (SELECT 1 FROM payout_item_entries l JOIN payout_items i ON i.id = l.payout_item_id
			                WHERE l.entry_id = settlement_entries.id AND i.status IN ('pending', 'succeeded'))), 0)
		FROM settlement_entries WHERE vendor_id = $1 AND currency = $2`, vendorID, currency, from, to).Scan(&sales, &refunded, &paidOut, &eligibleNow)
	return sales, refunded, paidOut, eligibleNow, err
}
