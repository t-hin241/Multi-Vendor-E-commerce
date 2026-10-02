package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/middleware"
)

// InboxSQL creates a consuming service's inbox (copy into a migration of
// that service). One row per (consumer, event): processed rows dedup
// redeliveries; parked rows keep the event for replay or discard. Every
// operator action is in event_inbox_audit (append-only).
const InboxSQL = `
CREATE TABLE IF NOT EXISTS event_inbox (
    consumer TEXT NOT NULL CHECK (consumer ~ '^[a-z0-9_-]{1,64}$'),
    event_id TEXT NOT NULL CHECK (length(event_id) <= 128),
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('processed', 'parked', 'discarded')),
    attempts INTEGER NOT NULL DEFAULT 1,
    envelope JSONB,
    last_error TEXT CHECK (last_error IS NULL OR length(last_error) <= 500),
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ,
    parked_at TIMESTAMPTZ,
    PRIMARY KEY (consumer, event_id)
);
CREATE INDEX IF NOT EXISTS event_inbox_parked_idx ON event_inbox (parked_at) WHERE status = 'parked';
CREATE INDEX IF NOT EXISTS event_inbox_received_idx ON event_inbox (received_at) WHERE status = 'processed';
CREATE TABLE IF NOT EXISTS event_inbox_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id UUID NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('event_replayed', 'event_discarded')),
    consumer TEXT NOT NULL,
    event_id TEXT NOT NULL,
    reason TEXT NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
    request_id TEXT CHECK (request_id IS NULL OR length(request_id) <= 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS event_inbox_audit_recent_idx ON event_inbox_audit (created_at DESC, id);
CREATE OR REPLACE FUNCTION event_inbox_audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Audit rows are append-only';
END $$;
DROP TRIGGER IF EXISTS event_inbox_audit_append_only ON event_inbox_audit;
CREATE TRIGGER event_inbox_audit_append_only BEFORE UPDATE OR DELETE ON event_inbox_audit
FOR EACH ROW EXECUTE FUNCTION event_inbox_audit_append_only();
`

// InboxAuditSearchSQL exposes replay/discard to the admin audit search
// (pkg/adminaudit); a service adds it to its own search with UNION ALL.
const InboxAuditSearchSQL = `SELECT id::text, created_at, actor_id::text, action, 'event', consumer || ':' || event_id, reason, request_id,
	NULL::jsonb FROM event_inbox_audit`

// Handler applies one event in tx (the transaction that also records it in
// the inbox). Returning an error rolls both back.
type Handler func(ctx context.Context, tx pgx.Tx, env Envelope) error

// Inbox is a consuming service's inbox table.
type Inbox struct{ Pool *pgxpool.Pool }

var errDuplicate = errors.New("eventbus: event already handled")

// Process applies env through fn once per consumer: the inbox row and fn's
// writes commit together. A repeat (already processed, parked or
// discarded) does nothing.
func (in Inbox) Process(ctx context.Context, consumer string, env Envelope, fn Handler) error {
	err := in.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO event_inbox (consumer, event_id, event_type, aggregate_id, status, processed_at)
			VALUES ($1, $2, $3, $4, 'processed', now()) ON CONFLICT (consumer, event_id) DO NOTHING`,
			consumer, env.EventID, env.Type, env.AggregateID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errDuplicate
		}
		return fn(ctx, tx, env)
	})
	if errors.Is(err, errDuplicate) {
		return nil
	}
	return err
}

// Park keeps env (with why it failed) for an operator; a processed event is
// never turned back into a parked one.
func (in Inbox) Park(ctx context.Context, consumer string, env Envelope, attempts int, reason string) error {
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if len(reason) > 500 {
		reason = reason[:500]
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err = in.Pool.Exec(ctx, `INSERT INTO event_inbox (consumer, event_id, event_type, aggregate_id, status, attempts, envelope, last_error, parked_at)
		VALUES ($1, $2, $3, $4, 'parked', $5, $6, $7, now())
		ON CONFLICT (consumer, event_id) DO UPDATE SET status = 'parked', attempts = EXCLUDED.attempts, envelope = EXCLUDED.envelope,
			last_error = EXCLUDED.last_error, parked_at = now()
		WHERE event_inbox.status = 'parked'`, consumer, env.EventID, env.Type, env.AggregateID, attempts, raw, reason)
	return err
}

func (in Inbox) tx(ctx context.Context, fn func(pgx.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := in.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Parked is a parked event as an operator sees it (no payload: it may hold
// an address).
type Parked struct {
	Consumer    string    `json:"consumer"`
	EventID     string    `json:"event_id"`
	EventType   string    `json:"event_type"`
	AggregateID string    `json:"aggregate_id"`
	Attempts    int       `json:"attempts"`
	LastError   *string   `json:"last_error,omitempty"`
	ParkedAt    time.Time `json:"parked_at"`
}

func (in Inbox) ListParked(ctx context.Context, limit int) ([]Parked, error) {
	rows, err := in.Pool.Query(ctx, `SELECT consumer, event_id, event_type, aggregate_id, attempts, last_error, parked_at
		FROM event_inbox WHERE status = 'parked' ORDER BY parked_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Parked{}
	for rows.Next() {
		var p Parked
		if err := rows.Scan(&p.Consumer, &p.EventID, &p.EventType, &p.AggregateID, &p.Attempts, &p.LastError, &p.ParkedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Stats is the inbox's share of a service's operations report.
func (in Inbox) Stats(ctx context.Context) (map[string]int64, error) {
	var parked, oldest, processed24 int64
	err := in.Pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status = 'parked'),
		COALESCE(EXTRACT(EPOCH FROM now() - min(parked_at) FILTER (WHERE status = 'parked')), 0)::bigint,
		count(*) FILTER (WHERE status = 'processed' AND processed_at > now() - interval '24 hours')
		FROM event_inbox`).Scan(&parked, &oldest, &processed24)
	return map[string]int64{"events_parked": parked, "events_oldest_parked_seconds": oldest, "events_processed_24h": processed24}, err
}

// ErrNotParked: the event is not (or no longer) parked.
var ErrNotParked = errors.New("eventbus: event is not parked")

// Replay applies a parked event again through fn and records who did it
// and why; on failure nothing changes and the error is returned.
func (in Inbox) Replay(ctx context.Context, consumer, eventID, actorID, reason string, fn Handler) error {
	return in.tx(ctx, func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT envelope FROM event_inbox WHERE consumer = $1 AND event_id = $2 AND status = 'parked' FOR UPDATE`,
			consumer, eventID).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotParked
		}
		if err != nil {
			return err
		}
		var env Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return Permanent(fmt.Errorf("eventbus: stored envelope unreadable: %w", err))
		}
		if err := fn(ctx, tx, env); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE event_inbox SET status = 'processed', processed_at = now(), last_error = NULL
			WHERE consumer = $1 AND event_id = $2`, consumer, eventID); err != nil {
			return err
		}
		return audit(ctx, tx, actorID, "event_replayed", consumer, eventID, reason)
	})
}

// Discard gives up on a parked event (it will never be applied), audited.
func (in Inbox) Discard(ctx context.Context, consumer, eventID, actorID, reason string) error {
	return in.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE event_inbox SET status = 'discarded', processed_at = now()
			WHERE consumer = $1 AND event_id = $2 AND status = 'parked'`, consumer, eventID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotParked
		}
		return audit(ctx, tx, actorID, "event_discarded", consumer, eventID, reason)
	})
}

func audit(ctx context.Context, tx pgx.Tx, actorID, action, consumer, eventID, reason string) error {
	_, err := tx.Exec(ctx, `INSERT INTO event_inbox_audit (actor_id, action, consumer, event_id, reason, request_id) VALUES ($1, $2, $3, $4, $5, $6)`,
		actorID, action, consumer, eventID, reason, middleware.CorrelationID(ctx))
	return err
}

// PurgeProcessed deletes processed/discarded rows older than keep (the
// dedup window must stay longer than the stream's 14-day retention).
func (in Inbox) PurgeProcessed(ctx context.Context, keep time.Duration) (int64, error) {
	tag, err := in.Pool.Exec(ctx, `DELETE FROM event_inbox WHERE status IN ('processed', 'discarded') AND processed_at < $1`, time.Now().Add(-keep))
	return tag.RowsAffected(), err
}
