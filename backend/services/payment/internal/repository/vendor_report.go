package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/vendorreport"
)

type VendorReport struct{ Pool *pgxpool.Pool }

func (r VendorReport) Report(ctx context.Context, id string, period vendorreport.Range) (*vendorreport.Report, error) {
	out := &vendorreport.Report{Range: period, AsOf: time.Now().UTC(), Captured: vendorreport.Unknown("vendor_allocation_not_available"), Refunded: vendorreport.Unknown("vendor_refund_allocation_not_available"), Eligible: vendorreport.Unknown("settlement_eligibility_not_confirmed")}
	var paid, legacy int64
	err := r.Pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount) FILTER (WHERE currency=$4 AND status='succeeded'),0),COUNT(*) FILTER (WHERE currency IS NULL AND status='succeeded') FROM payout_items WHERE vendor_id=$1 AND updated_at >= $2 AND updated_at < $3`, id, period.From, period.To, period.Currency).Scan(&paid, &legacy)
	out.PaidOut = vendorreport.Known(paid)
	if legacy > 0 {
		out.PaidOut = vendorreport.Unknown("legacy_payout_currency_requires_review")
	}
	return out, err
}
