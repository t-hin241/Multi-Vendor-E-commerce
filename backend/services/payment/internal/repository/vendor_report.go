package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/vendorreport"
)

// VendorReport answers the vendor dashboard from the settlement ledger.
// Captured stays unknown: Payment records a vendor's share when the order
// completes, not per capture.
type VendorReport struct{ Pool *pgxpool.Pool }

func (r VendorReport) Report(ctx context.Context, id string, period vendorreport.Range) (*vendorreport.Report, error) {
	out := &vendorreport.Report{Range: period, AsOf: time.Now().UTC(), Captured: vendorreport.Unknown("vendor_allocation_not_available")}
	_, refunded, paidOut, eligible, err := (&SettlementRepository{pool: r.Pool}).Report(ctx, id, period.Currency, period.From, period.To)
	if err != nil {
		return nil, err
	}
	out.Refunded = vendorreport.Known(refunded)
	// Before Order's return/refund holds, which are checked when a payout
	// batch is created.
	out.Eligible = vendorreport.Known(eligible)
	out.PaidOut = vendorreport.Known(paidOut)
	var legacy int64
	if err := r.Pool.QueryRow(ctx, `SELECT count(*) FROM payout_items WHERE vendor_id = $1 AND status = 'succeeded' AND currency IS NULL`, id).Scan(&legacy); err != nil {
		return nil, err
	}
	if legacy > 0 {
		out.PaidOut = vendorreport.Unknown("legacy_payout_currency_requires_review")
	}
	return out, nil
}
