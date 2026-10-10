package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/noticeoutbox"
)

// BuyerNotices is the outbox of buyer notices without an order (PW-009).
type BuyerNotices struct{ Pool *pgxpool.Pool }

func (n BuyerNotices) Outbox() noticeoutbox.Outbox {
	return noticeoutbox.Outbox{Pool: n.Pool, Table: "order_buyer_notices"}
}

// Queue records a notice in the caller's transaction, once per dedupKey.
func (n BuyerNotices) Queue(ctx context.Context, dedupKey, userID, noticeType, referenceID string) error {
	return n.Outbox().Enqueue(ctx, connection(ctx, n.Pool), dedupKey, userID, noticeType, referenceID)
}

// Drain relays due notices, at most limit.
func (n BuyerNotices) Drain(ctx context.Context, limit int, deliver func(context.Context, noticeoutbox.Notice) error) (int, error) {
	return n.Outbox().Drain(ctx, limit, deliver)
}

// Pending counts notices not relayed yet and those needing review.
func (n BuyerNotices) Pending(ctx context.Context) (waiting, review int64, err error) {
	return n.Outbox().Pending(ctx)
}
