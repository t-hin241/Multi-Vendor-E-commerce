package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// VendorNoticeBackfill queues notify_vendor effects for open shop work
// (PW-045), with the targets the transitions use ("<kind>:<reference>"),
// so ON CONFLICT drops what was already queued.
type VendorNoticeBackfill struct{ Pool *pgxpool.Pool }

const vendorNoticeBackfillSQL = `
WITH work AS (
	SELECT vo.order_id, vo.vendor_id, vo.id AS vendor_order_id, 'new_order' AS kind, vo.id AS reference_id
	FROM vendor_orders vo WHERE vo.status IN ('paid', 'processing')
	UNION ALL
	SELECT c.order_id, c.vendor_id, c.vendor_order_id, 'cancellation_requested', c.id
	FROM cancellation_requests c WHERE c.origin = 'buyer' AND c.status IN ('preparing', 'requested', 'stopping_fulfillment')
	UNION ALL
	SELECT r.order_id, vo.vendor_id, vo.id, CASE WHEN r.status = 'requested' THEN 'return_requested' ELSE 'return_dispatched' END, r.id
	FROM return_requests r JOIN order_items i ON i.id = r.order_item_id JOIN vendor_orders vo ON vo.id = i.vendor_order_id
	WHERE r.status = 'requested' OR (r.status = 'approved' AND r.shipping_status = 'awaiting_verification')
)
INSERT INTO order_effects (order_id, kind, target, payload)
SELECT order_id, 'notify_vendor', kind || ':' || reference_id,
	jsonb_build_object('vendor_id', vendor_id, 'vendor_order_id', vendor_order_id, 'action_kind', kind, 'reference_id', reference_id, 'backfill', true)
FROM work
ON CONFLICT (order_id, kind, target) DO NOTHING`

// BackfillVendorNotices returns how many notices were queued.
func (b VendorNoticeBackfill) BackfillVendorNotices(ctx context.Context) (int64, error) {
	tag, err := b.Pool.Exec(ctx, vendorNoticeBackfillSQL)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
