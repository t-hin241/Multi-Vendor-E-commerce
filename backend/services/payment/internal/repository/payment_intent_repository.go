package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/payment/internal/domain"
)

var ErrPaymentIntentNotFound = errors.New("repository: payment intent not found")

const paymentIntentColumns = `id, order_id, buyer_id, amount, currency, status, provider, provider_intent_id, failure_reason, created_at, updated_at`

type PaymentIntentRepository struct {
	pool *pgxpool.Pool
}

func NewPaymentIntentRepository(pool *pgxpool.Pool) *PaymentIntentRepository {
	return &PaymentIntentRepository{pool: pool}
}

func (r *PaymentIntentRepository) Create(ctx context.Context, intent *domain.PaymentIntent) error {
	const query = `
		INSERT INTO payment_intents (order_id, buyer_id, amount, currency, status, provider, provider_intent_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at`

	return r.pool.QueryRow(ctx, query,
		intent.OrderID, intent.BuyerID, intent.Amount, intent.Currency, intent.Status, intent.Provider, intent.ProviderIntentID,
	).Scan(&intent.ID, &intent.CreatedAt, &intent.UpdatedAt)
}

func scanPaymentIntent(row pgx.Row) (*domain.PaymentIntent, error) {
	var i domain.PaymentIntent
	err := row.Scan(&i.ID, &i.OrderID, &i.BuyerID, &i.Amount, &i.Currency, &i.Status, &i.Provider, &i.ProviderIntentID, &i.FailureReason, &i.CreatedAt, &i.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentIntentNotFound
		}
		return nil, err
	}
	return &i, nil
}

func (r *PaymentIntentRepository) FindByID(ctx context.Context, id string) (*domain.PaymentIntent, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+paymentIntentColumns+` FROM payment_intents WHERE id = $1`, id)
	return scanPaymentIntent(row)
}

func (r *PaymentIntentRepository) FindByProviderIntentID(ctx context.Context, providerIntentID string) (*domain.PaymentIntent, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+paymentIntentColumns+` FROM payment_intents WHERE provider_intent_id = $1`, providerIntentID)
	return scanPaymentIntent(row)
}

// FindPendingByOrderID returns the most recent still-pending intent for an
// order, if any, so re-initiating payment for the same order reuses it
// instead of creating a duplicate the buyer could end up paying twice.
func (r *PaymentIntentRepository) FindPendingByOrderID(ctx context.Context, orderID string) (*domain.PaymentIntent, error) {
	const query = `SELECT ` + paymentIntentColumns + ` FROM payment_intents WHERE order_id = $1 AND status = 'pending' ORDER BY created_at DESC LIMIT 1`
	row := r.pool.QueryRow(ctx, query, orderID)
	return scanPaymentIntent(row)
}

func (r *PaymentIntentRepository) UpdateStatus(ctx context.Context, id string, status domain.Status, failureReason *string) error {
	const query = `UPDATE payment_intents SET status = $1, failure_reason = $2, updated_at = now() WHERE id = $3`
	tag, err := r.pool.Exec(ctx, query, status, failureReason, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPaymentIntentNotFound
	}
	return nil
}
