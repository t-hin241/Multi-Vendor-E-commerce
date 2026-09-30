package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/payment/internal/domain"
)

var ErrReceiptNotFound = errors.New("repository: receipt not found")

const receiptColumns = `id, provider, provider_event_id, COALESCE(provider_intent_id, ''), COALESCE(provider_reference, ''), payment_intent_id,
	event_type, COALESCE(amount, 0), COALESCE(currency, ''), COALESCE(failure_reason, ''), status, outcome, attempts, last_error, received_at, processed_at`

// ReceiptRepository stores every verified provider event before it is
// applied, so a crash between receiving and applying never loses it.
type ReceiptRepository struct{ pool *pgxpool.Pool }

func NewReceiptRepository(pool *pgxpool.Pool) *ReceiptRepository {
	return &ReceiptRepository{pool: pool}
}

func scanReceipt(row pgx.Row) (*domain.Receipt, error) {
	var r domain.Receipt
	err := row.Scan(&r.ID, &r.Provider, &r.ProviderEventID, &r.ProviderIntentID, &r.ProviderReference, &r.PaymentIntentID,
		&r.EventType, &r.Amount, &r.Currency, &r.FailureReason, &r.Status, &r.Outcome, &r.Attempts, &r.LastError, &r.ReceivedAt, &r.ProcessedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrReceiptNotFound
	}
	return &r, err
}

func (r *ReceiptRepository) list(ctx context.Context, query string, args ...any) ([]*domain.Receipt, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.Receipt{}
	for rows.Next() {
		rc, err := scanReceipt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}

// Record stores a verified event once per (provider, event id). It returns
// the stored receipt and whether this call created it.
func (r *ReceiptRepository) Record(ctx context.Context, rc *domain.Receipt) (*domain.Receipt, bool, error) {
	stored, err := scanReceipt(connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO payment_receipts (provider, provider_event_id, provider_intent_id, provider_reference, event_type, amount, currency, failure_reason)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5, $6, NULLIF($7, ''), NULLIF($8, ''))
		ON CONFLICT (provider, provider_event_id) DO NOTHING
		RETURNING `+receiptColumns,
		rc.Provider, rc.ProviderEventID, rc.ProviderIntentID, rc.ProviderReference, rc.EventType, rc.Amount, rc.Currency, rc.FailureReason))
	if err == nil {
		return stored, true, nil
	}
	if !errors.Is(err, ErrReceiptNotFound) {
		return nil, false, err
	}
	stored, err = scanReceipt(connection(ctx, r.pool).QueryRow(ctx,
		`SELECT `+receiptColumns+` FROM payment_receipts WHERE provider = $1 AND provider_event_id = $2`, rc.Provider, rc.ProviderEventID))
	return stored, false, err
}

// Lock reads a receipt for update, skipping one another worker holds.
func (r *ReceiptRepository) Lock(ctx context.Context, id string) (*domain.Receipt, error) {
	return scanReceipt(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+receiptColumns+` FROM payment_receipts WHERE id = $1 FOR UPDATE SKIP LOCKED`, id))
}

func (r *ReceiptRepository) FindByID(ctx context.Context, id string) (*domain.Receipt, error) {
	return scanReceipt(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+receiptColumns+` FROM payment_receipts WHERE id = $1`, id))
}

// Finish records the final status of an applied (or refused) receipt.
func (r *ReceiptRepository) Finish(ctx context.Context, id string, intentID *string, status domain.ReceiptStatus, outcome string) error {
	_, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE payment_receipts SET status = $2, outcome = $3, payment_intent_id = COALESCE($4, payment_intent_id),
			attempts = attempts + 1, last_error = NULL, processed_at = now()
		WHERE id = $1`, id, status, outcome, intentID)
	return err
}

// Park keeps a receipt with no matching intent for a later retry.
func (r *ReceiptRepository) Park(ctx context.Context, id string) error {
	_, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE payment_receipts SET status = 'parked', outcome = 'unknown_intent', attempts = attempts + 1,
			next_attempt_at = now() + interval '5 minutes'
		WHERE id = $1`, id)
	return err
}

// MarkRetryable records a failed apply; the worker retries with backoff.
func (r *ReceiptRepository) MarkRetryable(ctx context.Context, id, message string) error {
	_, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE payment_receipts SET status = 'retryable', attempts = attempts + 1, last_error = left($2, 500),
			next_attempt_at = now() + interval '1 second' * least(600, power(2, attempts))
		WHERE id = $1 AND status NOT IN ('processed', 'rejected')`, id, message)
	return err
}

// Requeue makes a receipt due now (admin retry).
func (r *ReceiptRepository) Requeue(ctx context.Context, id string) error {
	tag, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE payment_receipts SET status = 'retryable', next_attempt_at = now(), received_at = received_at
		WHERE id = $1 AND status IN ('retryable', 'parked', 'received')`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleState
	}
	return nil
}

// ListDue returns receipts to (re)apply: received ones the handler did not
// finish, retryable ones, and parked ones still inside the retry window.
func (r *ReceiptRepository) ListDue(ctx context.Context, limit int) ([]*domain.Receipt, error) {
	return r.list(ctx, `SELECT `+receiptColumns+` FROM payment_receipts
		WHERE next_attempt_at <= now()
		  AND ((status = 'received' AND received_at < now() - interval '30 seconds')
		    OR status = 'retryable'
		    OR (status = 'parked' AND received_at > now() - $1::interval))
		ORDER BY next_attempt_at LIMIT $2`, domain.ParkedRetryWindow.String(), limit)
}

// ListByStatus lists receipts in one status, newest first.
func (r *ReceiptRepository) ListByStatus(ctx context.Context, status domain.ReceiptStatus, limit, offset int) ([]*domain.Receipt, error) {
	return r.list(ctx, `SELECT `+receiptColumns+` FROM payment_receipts WHERE status = $1 ORDER BY received_at DESC LIMIT $2 OFFSET $3`, status, limit, offset)
}

// Search finds receipts by event id, provider intent id, reference or intent.
func (r *ReceiptRepository) Search(ctx context.Context, q string, limit int) ([]*domain.Receipt, error) {
	return r.list(ctx, `SELECT `+receiptColumns+` FROM payment_receipts
		WHERE provider_event_id = $1 OR provider_intent_id = $1 OR provider_reference = $1 OR payment_intent_id::text = $1 OR id::text = $1
		ORDER BY received_at DESC LIMIT $2`, q, limit)
}

// Counts reports receipts needing attention.
func (r *ReceiptRepository) Counts(ctx context.Context) (map[string]int64, error) {
	var parked, rejected, retryable, stale int64
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status = 'parked'),
		count(*) FILTER (WHERE status = 'rejected'),
		count(*) FILTER (WHERE status = 'retryable'),
		count(*) FILTER (WHERE status = 'received' AND received_at < now() - interval '5 minutes')
		FROM payment_receipts WHERE status <> 'processed'`).Scan(&parked, &rejected, &retryable, &stale)
	return map[string]int64{"receipts_parked": parked, "receipts_rejected": rejected, "receipts_retryable": retryable, "receipts_unapplied": stale}, err
}

// RecordLegacyEvent keeps the previous version's dedup table in step so a
// rollback does not re-apply events this version already handled.
func (r *ReceiptRepository) RecordLegacyEvent(ctx context.Context, providerEventID, intentID string, eventType domain.EventType) error {
	_, err := connection(ctx, r.pool).Exec(ctx, `
		INSERT INTO payment_events (provider_event_id, payment_intent_id, event_type) VALUES ($1, $2, $3)
		ON CONFLICT (provider_event_id) DO NOTHING`, providerEventID, intentID, eventType)
	return err
}

// OldestUnprocessed is for the operations report.
func (r *ReceiptRepository) OldestUnprocessed(ctx context.Context) (*time.Time, error) {
	var t *time.Time
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT min(received_at) FROM payment_receipts WHERE status IN ('received', 'retryable')`).Scan(&t)
	return t, err
}
