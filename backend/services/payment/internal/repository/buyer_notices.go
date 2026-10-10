package repository

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/noticeoutbox"
)

// Buyer notice types (PW-009, AF-06); the reference is the order id.
const (
	NoticeRefundDestinationNeeded   = "refund_destination_needed"
	NoticeRefundDestinationRejected = "refund_destination_rejected"
)

// BuyerNotices is the outbox of refund facts the buyer has to act on.
type BuyerNotices struct{ Pool *pgxpool.Pool }

func (n BuyerNotices) Outbox() noticeoutbox.Outbox {
	return noticeoutbox.Outbox{Pool: n.Pool, Table: "payment_buyer_notices"}
}

// DestinationRejected queues, in the caller's transaction, the notice that
// destination version was rejected; once per version.
func (n BuyerNotices) DestinationRejected(ctx context.Context, refundID string, version int) error {
	q := connection(ctx, n.Pool)
	var buyer, order string
	if err := q.QueryRow(ctx, `SELECT p.buyer_id::text, f.order_id::text FROM payment_refunds f
		JOIN payment_intents p ON p.id = f.payment_intent_id WHERE f.id = $1`, refundID).Scan(&buyer, &order); err != nil {
		return err
	}
	return n.Outbox().Enqueue(ctx, q, NoticeRefundDestinationRejected+":"+refundID+":v"+strconv.Itoa(version), buyer, NoticeRefundDestinationRejected, order)
}

// QueueMissingDestinations tells the buyer of each open refund that has
// never had a destination to give one; once per refund, so turning the
// manual workflow on also reaches refunds opened before.
func (n BuyerNotices) QueueMissingDestinations(ctx context.Context, limit int) (int64, error) {
	tag, err := n.Pool.Exec(ctx, `INSERT INTO payment_buyer_notices (dedup_key, user_id, notice_type, reference_id)
		SELECT 'refund_destination_needed:' || f.id, p.buyer_id, 'refund_destination_needed', f.order_id::text
		FROM payment_refunds f JOIN payment_intents p ON p.id = f.payment_intent_id
		WHERE f.status IN ('awaiting_provider_refund', 'pending')
		  AND NOT EXISTS (SELECT 1 FROM refund_destinations d WHERE d.refund_id = f.id)
		  AND NOT EXISTS (SELECT 1 FROM payment_buyer_notices b WHERE b.dedup_key = 'refund_destination_needed:' || f.id)
		ORDER BY f.created_at LIMIT $1
		ON CONFLICT (dedup_key) DO NOTHING`, limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
