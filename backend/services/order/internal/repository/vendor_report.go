package repository

import (
	"context"
	"time"

	"shopee/backend/pkg/vendorreport"
)

func (r *VendorOrderRepository) Report(ctx context.Context, id string, period vendorreport.Range) (*vendorreport.Report, error) {
	out := &vendorreport.Report{Range: period, AsOf: time.Now().UTC()}
	var gross int64
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*),COALESCE(SUM(subtotal_amount),0),COUNT(*) FILTER (WHERE status IN ('paid','processing')) FROM vendor_orders WHERE vendor_id=$1 AND created_at >= $2 AND created_at < $3 AND currency=$4`, id, period.From, period.To, period.Currency).Scan(&out.TotalOrders, &gross, &out.AwaitingFulfillment)
	out.GrossOrdered = vendorreport.Known(gross)
	return out, err
}
