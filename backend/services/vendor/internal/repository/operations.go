package repository

import (
	"context"

	"shopee/backend/services/vendorsvc/internal/domain"
)

func (r *VendorRepository) Operations(ctx context.Context) (*domain.OperationsSummary, error) {
	var out domain.OperationsSummary
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT
 (SELECT count(*) FROM vendors WHERE status='pending'),
 (SELECT count(*) FROM vendor_outbox WHERE delivered_at IS NULL),
 (SELECT count(*) FROM vendor_outbox WHERE delivered_at IS NULL AND attempts>=10),
 (SELECT COALESCE(EXTRACT(EPOCH FROM now()-MIN(created_at)),0)::double precision FROM vendor_outbox WHERE delivered_at IS NULL),
 (SELECT count(*) FROM vendor_audit_logs WHERE action='payout_submitted' AND created_at>now()-interval '24 hours'),
 (SELECT count(*) FROM vendor_notification_outbox WHERE delivered_at IS NULL AND parked_at IS NULL),
 (SELECT count(*) FROM vendor_notification_outbox WHERE parked_at IS NOT NULL AND delivered_at IS NULL)`).Scan(&out.PendingApplications, &out.PendingStatusEvents, &out.ParkedStatusEvents, &out.OldestPendingEventSeconds, &out.PayoutChanges24Hours,
		&out.PendingNotices, &out.ParkedNotices)
	return &out, err
}
