package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NotificationOutbox holds shop-decision notices until Notification has
// accepted them.
type NotificationOutbox struct{ Pool *pgxpool.Pool }

// Notice is one queued notice.
type Notice struct {
	ID, VendorID, UserID, Type string
	Attempts                   int
}

// MaxNoticeAttempts bounds retries (backoff up to 10 minutes: about a day).
const MaxNoticeAttempts = 150

// Queue adds the notice in the caller's transaction (once per shop version
// and type).
func (r NotificationOutbox) Queue(ctx context.Context, vendorID, userID, notifType string, version int64) error {
	_, err := connection(ctx, r.Pool).Exec(ctx, `INSERT INTO vendor_notification_outbox (vendor_id, user_id, type, version)
		VALUES ($1, $2, $3, $4) ON CONFLICT (vendor_id, version, type) DO NOTHING`, vendorID, userID, notifType, version)
	return err
}

func (r NotificationOutbox) Claim(ctx context.Context) ([]Notice, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := r.Pool.Query(ctx, `UPDATE vendor_notification_outbox SET lease_until = now() + interval '60 seconds', attempts = attempts + 1
		WHERE id IN (SELECT id FROM vendor_notification_outbox
			WHERE delivered_at IS NULL AND parked_at IS NULL AND next_attempt_at <= now() AND (lease_until IS NULL OR lease_until < now())
			ORDER BY created_at LIMIT 10 FOR UPDATE SKIP LOCKED)
		RETURNING id, vendor_id, user_id, type, attempts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notice
	for rows.Next() {
		var n Notice
		if err := rows.Scan(&n.ID, &n.VendorID, &n.UserID, &n.Type, &n.Attempts); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Delivered marks the notice accepted by Notification.
func (r NotificationOutbox) Delivered(ctx context.Context, id string) error {
	_, err := r.Pool.Exec(ctx, `UPDATE vendor_notification_outbox SET delivered_at = now(), lease_until = NULL, last_error = NULL WHERE id = $1`, id)
	return err
}

// Failed schedules a retry with backoff, or parks the notice when it was
// refused or ran out of attempts.
func (r NotificationOutbox) Failed(ctx context.Context, n Notice, reason string, refused bool) error {
	_, err := r.Pool.Exec(ctx, `UPDATE vendor_notification_outbox SET lease_until = NULL, last_error = left($2, 300),
		next_attempt_at = now() + LEAST(5 * power(2, LEAST(attempts, 7)), 600) * interval '1 second',
		parked_at = CASE WHEN $3 OR attempts >= $4 THEN now() END
		WHERE id = $1`, n.ID, reason, refused, MaxNoticeAttempts)
	return err
}
