package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/provider"
)

var (
	ErrPaymentIntentNotFound = errors.New("repository: payment intent not found")
	// ErrOpenIntentExists: the order already has a creating or pending intent.
	ErrOpenIntentExists = errors.New("repository: order already has an open payment intent")
)

const paymentIntentColumns = `id, order_id, buyer_id, amount, currency, status, provider, provider_intent_id, provider_reference,
	checkout_url, qr_code, expires_at, failure_reason, create_attempts, last_error, closed_reason, created_at, updated_at`

type PaymentIntentRepository struct {
	pool *pgxpool.Pool
}

func NewPaymentIntentRepository(pool *pgxpool.Pool) *PaymentIntentRepository {
	return &PaymentIntentRepository{pool: pool}
}

func scanPaymentIntent(row pgx.Row) (*domain.PaymentIntent, error) {
	var i domain.PaymentIntent
	var providerIntentID, reference *string
	err := row.Scan(&i.ID, &i.OrderID, &i.BuyerID, &i.Amount, &i.Currency, &i.Status, &i.Provider, &providerIntentID, &reference,
		&i.CheckoutURL, &i.QRCode, &i.ExpiresAt, &i.FailureReason, &i.CreateAttempts, &i.LastError, &i.ClosedReason, &i.CreatedAt, &i.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPaymentIntentNotFound
	}
	if err != nil {
		return nil, err
	}
	if providerIntentID != nil {
		i.ProviderIntentID = *providerIntentID
	}
	if reference != nil {
		i.ProviderReference = *reference
	}
	return &i, nil
}

func (r *PaymentIntentRepository) list(ctx context.Context, query string, args ...any) ([]*domain.PaymentIntent, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.PaymentIntent{}
	for rows.Next() {
		i, err := scanPaymentIntent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// NextOrderCode reserves a payOS orderCode.
func (r *PaymentIntentRepository) NextOrderCode(ctx context.Context) (int64, error) {
	var code int64
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT nextval('payment_provider_order_code_seq')`).Scan(&code)
	return code, err
}

// CreateOperation persists an intent in 'creating' before the provider is
// called. The order's single open intent is enforced by a unique index.
func (r *PaymentIntentRepository) CreateOperation(ctx context.Context, intent *domain.PaymentIntent) error {
	err := connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO payment_intents (order_id, buyer_id, amount, currency, status, provider, provider_reference, expires_at)
		VALUES ($1, $2, $3, $4, 'creating', $5, $6, $7)
		RETURNING id, status, created_at, updated_at`,
		intent.OrderID, intent.BuyerID, intent.Amount, intent.Currency, intent.Provider, intent.ProviderReference, intent.ExpiresAt,
	).Scan(&intent.ID, &intent.Status, &intent.CreatedAt, &intent.UpdatedAt)
	if isUniqueViolation(err, "payment_intents_open_order_key") {
		return ErrOpenIntentExists
	}
	return err
}

// Create inserts an intent as given (tests and legacy data).
func (r *PaymentIntentRepository) Create(ctx context.Context, intent *domain.PaymentIntent) error {
	return connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO payment_intents (order_id, buyer_id, amount, currency, status, provider, provider_intent_id, provider_reference, checkout_url, qr_code, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), $9, $10, $11)
		RETURNING id, created_at, updated_at`,
		intent.OrderID, intent.BuyerID, intent.Amount, intent.Currency, intent.Status, intent.Provider, intent.ProviderIntentID,
		intent.ProviderReference, intent.CheckoutURL, intent.QRCode, intent.ExpiresAt,
	).Scan(&intent.ID, &intent.CreatedAt, &intent.UpdatedAt)
}

// MarkLinked records the provider link of a 'creating' intent.
func (r *PaymentIntentRepository) MarkLinked(ctx context.Context, id string, link provider.CreateIntentResult) error {
	tag, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE payment_intents SET status = 'pending', provider_intent_id = $2, checkout_url = $3, qr_code = $4,
			expires_at = COALESCE($5, expires_at), last_error = NULL, updated_at = now()
		WHERE id = $1 AND status = 'creating'`, id, link.ProviderIntentID, link.CheckoutURL, link.QRCode, link.ExpiresAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleState
	}
	return nil
}

// RecordCreateFailure notes a failed provider call on a 'creating' intent.
func (r *PaymentIntentRepository) RecordCreateFailure(ctx context.Context, id, message string) error {
	_, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE payment_intents SET create_attempts = create_attempts + 1, last_error = left($2, 500), updated_at = now()
		WHERE id = $1`, id, message)
	return err
}

// Close marks an open intent expired: its link can no longer be paid.
func (r *PaymentIntentRepository) Close(ctx context.Context, id, reason string) error {
	tag, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE payment_intents SET status = 'expired', closed_reason = $2, updated_at = now()
		WHERE id = $1 AND status IN ('creating', 'pending')`, id, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleState
	}
	return nil
}

// Transition moves an intent from one status to another (compare-and-set).
// The status trigger queues captured/failed outcomes for Order in the same
// transaction.
func (r *PaymentIntentRepository) Transition(ctx context.Context, id string, from, to domain.Status, failureReason *string) error {
	tag, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE payment_intents SET status = $3, failure_reason = COALESCE($4, failure_reason), updated_at = now()
		WHERE id = $1 AND status = $2`, id, from, to, failureReason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleState
	}
	return nil
}

// Touch records that reconciliation looked at the intent.
func (r *PaymentIntentRepository) Touch(ctx context.Context, id string) error {
	_, err := connection(ctx, r.pool).Exec(ctx, `UPDATE payment_intents SET last_checked_at = now() WHERE id = $1`, id)
	return err
}

func (r *PaymentIntentRepository) FindByID(ctx context.Context, id string) (*domain.PaymentIntent, error) {
	return scanPaymentIntent(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+paymentIntentColumns+` FROM payment_intents WHERE id = $1`, id))
}

// LockByID reads the intent for update inside a transaction.
func (r *PaymentIntentRepository) LockByID(ctx context.Context, id string) (*domain.PaymentIntent, error) {
	return scanPaymentIntent(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+paymentIntentColumns+` FROM payment_intents WHERE id = $1 FOR UPDATE`, id))
}

// LockForEvent finds the intent a provider event belongs to, by the
// provider's intent id or by Payment's reference (the reference already
// exists while the intent is still 'creating').
func (r *PaymentIntentRepository) LockForEvent(ctx context.Context, providerName, providerIntentID, reference string) (*domain.PaymentIntent, error) {
	return scanPaymentIntent(connection(ctx, r.pool).QueryRow(ctx, `
		SELECT `+paymentIntentColumns+` FROM payment_intents
		WHERE provider = $1 AND ((provider_intent_id = $2 AND $2 <> '') OR (provider_reference = $3 AND $3 <> ''))
		ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, providerName, providerIntentID, reference))
}

func (r *PaymentIntentRepository) FindByProviderIntentID(ctx context.Context, providerIntentID string) (*domain.PaymentIntent, error) {
	return scanPaymentIntent(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+paymentIntentColumns+` FROM payment_intents WHERE provider_intent_id = $1`, providerIntentID))
}

// FindOpenByOrderID returns the order's creating or pending intent.
func (r *PaymentIntentRepository) FindOpenByOrderID(ctx context.Context, orderID string) (*domain.PaymentIntent, error) {
	return scanPaymentIntent(connection(ctx, r.pool).QueryRow(ctx,
		`SELECT `+paymentIntentColumns+` FROM payment_intents WHERE order_id = $1 AND status IN ('creating', 'pending')`, orderID))
}

// FindPendingByOrderID is kept for callers that only need a payable link.
func (r *PaymentIntentRepository) FindPendingByOrderID(ctx context.Context, orderID string) (*domain.PaymentIntent, error) {
	return scanPaymentIntent(connection(ctx, r.pool).QueryRow(ctx,
		`SELECT `+paymentIntentColumns+` FROM payment_intents WHERE order_id = $1 AND status = 'pending' ORDER BY created_at DESC LIMIT 1`, orderID))
}

// ListForReconciliation returns intents whose provider link must be checked:
// 'creating' for longer than creatingAfter, and 'pending' past their expiry
// by expiredAfter. Recently checked intents are skipped.
func (r *PaymentIntentRepository) ListForReconciliation(ctx context.Context, creatingAfter, expiredAfter time.Duration, limit int) ([]*domain.PaymentIntent, error) {
	return r.list(ctx, `SELECT `+paymentIntentColumns+` FROM payment_intents
		WHERE ((status = 'creating' AND updated_at < now() - $1::interval)
		    OR (status = 'pending' AND expires_at IS NOT NULL AND expires_at < now() - $2::interval))
		  AND (last_checked_at IS NULL OR last_checked_at < now() - interval '1 minute')
		ORDER BY updated_at LIMIT $3`, creatingAfter.String(), expiredAfter.String(), limit)
}

// Search finds intents by id, order id, provider id or reference.
func (r *PaymentIntentRepository) Search(ctx context.Context, q string, limit int) ([]*domain.PaymentIntent, error) {
	return r.list(ctx, `SELECT `+paymentIntentColumns+` FROM payment_intents
		WHERE id::text = $1 OR order_id::text = $1 OR provider_intent_id = $1 OR provider_reference = $1
		ORDER BY created_at DESC LIMIT $2`, q, limit)
}

// ListStuckCreating counts intents stuck before their provider link.
func (r *PaymentIntentRepository) Counts(ctx context.Context) (creating, expiredOpen int64, err error) {
	err = connection(ctx, r.pool).QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status = 'creating' AND updated_at < now() - interval '5 minutes'),
		count(*) FILTER (WHERE status = 'pending' AND expires_at < now() - interval '15 minutes')
		FROM payment_intents WHERE status IN ('creating', 'pending')`).Scan(&creating, &expiredOpen)
	return creating, expiredOpen, err
}
