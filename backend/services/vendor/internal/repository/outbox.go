package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/vendorsales"
	"shopee/backend/services/vendorsvc/internal/domain"
)

type Outbox struct{ Pool *pgxpool.Pool }
type Delivery struct {
	ID string
	vendorsales.Status
	Attempts int
}

func (r Outbox) Queue(ctx context.Context, v *domain.Vendor) error {
	_, err := connection(ctx, r.Pool).Exec(ctx, `INSERT INTO vendor_outbox(vendor_id,version,status) VALUES($1,$2,$3) ON CONFLICT(vendor_id,version) DO NOTHING`, v.ID, v.Version, v.Status)
	return err
}
func (r Outbox) Claim(ctx context.Context) ([]Delivery, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := r.Pool.Query(ctx, `UPDATE vendor_outbox SET lease_until=now()+interval '60 seconds',attempts=attempts+1 WHERE id IN
 (SELECT id FROM vendor_outbox WHERE delivered_at IS NULL AND attempts<10 AND next_attempt_at<=now() AND (lease_until IS NULL OR lease_until<now()) ORDER BY created_at LIMIT 5 FOR UPDATE SKIP LOCKED)
 RETURNING id,vendor_id,status,version,attempts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		var d Delivery
		if err = rows.Scan(&d.ID, &d.VendorID, &d.Status.Status, &d.Version, &d.Attempts); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (r Outbox) Complete(ctx context.Context, d Delivery, ok bool) error {
	return (Transactions{Pool: r.Pool}).Run(ctx, func(ctx context.Context) error {
		if !ok {
			_, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE vendor_outbox SET lease_until=NULL,next_attempt_at=now()+LEAST(attempts*attempts,300)*interval '1 second' WHERE id=$1`, d.ID)
			return err
		}
		if _, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE vendor_outbox SET delivered_at=now(),lease_until=NULL,catalog_delivered=true,order_delivered=true WHERE id=$1`, d.ID); err != nil {
			return err
		}
		_, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE vendors SET enforced_version=GREATEST(enforced_version,$2) WHERE id=$1`, d.VendorID, d.Version)
		return err
	})
}
func (r Outbox) Replay(ctx context.Context, vendorID string) error {
	_, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE vendor_outbox SET attempts=0,next_attempt_at=now(),lease_until=NULL WHERE vendor_id=$1 AND delivered_at IS NULL AND (lease_until IS NULL OR lease_until<now())`, vendorID)
	return err
}
