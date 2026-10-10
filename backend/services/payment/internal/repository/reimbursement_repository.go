package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/payment/internal/domain"
)

// ErrReimbursementNotFound: no such reimbursement (or no captured payment
// of the order to name its buyer).
var ErrReimbursementNotFound = errors.New("repository: reimbursement not found")

// ReimbursementRepository is the PW-032 book.
type ReimbursementRepository struct{ Pool *pgxpool.Pool }

const reimbursementColumns = `r.id, r.order_id, r.buyer_id, r.reason_code, r.reason, r.amount, r.currency, r.status, r.requested_by, r.decided_by,
	r.decided_at, r.decision_reason, r.destination_id, d.bank_code || ' ••••' || d.account_last4, r.bank_reference, r.bank_reference_key,
	r.paid_by, r.paid_at, r.idempotency_key, r.version, r.created_at, r.updated_at`

const reimbursementFrom = ` FROM reimbursements r LEFT JOIN refund_destinations d ON d.id = r.destination_id `

func scanReimbursement(row pgx.Row) (*domain.Reimbursement, error) {
	var r domain.Reimbursement
	err := row.Scan(&r.ID, &r.OrderID, &r.BuyerID, &r.ReasonCode, &r.Reason, &r.Amount, &r.Currency, &r.Status, &r.RequestedBy, &r.DecidedBy,
		&r.DecidedAt, &r.DecisionReason, &r.DestinationID, &r.DestinationMasked, &r.BankReference, &r.BankReferenceKey,
		&r.PaidBy, &r.PaidAt, &r.IdempotencyKey, &r.Version, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrReimbursementNotFound
	}
	return &r, err
}

// OrderBuyer is the buyer of the order's captured payment.
func (s ReimbursementRepository) OrderBuyer(ctx context.Context, orderID string) (string, error) {
	var buyer string
	err := connection(ctx, s.Pool).QueryRow(ctx, `SELECT buyer_id::text FROM payment_intents
		WHERE order_id = $1 AND status IN ('captured', 'refunded') ORDER BY created_at LIMIT 1`, orderID).Scan(&buyer)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrReimbursementNotFound
	}
	return buyer, err
}

// Create stores a request; a repeat of the requester's key returns the
// stored one (created false).
func (s ReimbursementRepository) Create(ctx context.Context, r *domain.Reimbursement) (bool, error) {
	q := connection(ctx, s.Pool)
	if r.IdempotencyKey != nil {
		existing, err := scanReimbursement(q.QueryRow(ctx, `SELECT `+reimbursementColumns+reimbursementFrom+
			`WHERE r.requested_by = $1 AND r.idempotency_key = $2`, r.RequestedBy, *r.IdempotencyKey))
		if err == nil {
			*r = *existing
			return false, nil
		}
		if !errors.Is(err, ErrReimbursementNotFound) {
			return false, err
		}
	}
	err := q.QueryRow(ctx, `INSERT INTO reimbursements (order_id, buyer_id, reason_code, reason, amount, currency, requested_by, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id, status, version, created_at, updated_at`,
		r.OrderID, r.BuyerID, r.ReasonCode, r.Reason, r.Amount, r.Currency, r.RequestedBy, r.IdempotencyKey).
		Scan(&r.ID, &r.Status, &r.Version, &r.CreatedAt, &r.UpdatedAt)
	if isUniqueViolation(err, "reimbursements_idempotency_idx") {
		return false, ErrStaleState
	}
	return err == nil, err
}

// Lock reads a reimbursement for update.
func (s ReimbursementRepository) Lock(ctx context.Context, id string) (*domain.Reimbursement, error) {
	return scanReimbursement(connection(ctx, s.Pool).QueryRow(ctx, `SELECT `+reimbursementColumns+reimbursementFrom+`WHERE r.id = $1 FOR UPDATE OF r`, id))
}

// Find reads one reimbursement.
func (s ReimbursementRepository) Find(ctx context.Context, id string) (*domain.Reimbursement, error) {
	return scanReimbursement(connection(ctx, s.Pool).QueryRow(ctx, `SELECT `+reimbursementColumns+reimbursementFrom+`WHERE r.id = $1`, id))
}

// Save writes a decision or a payment if the version is still expected.
func (s ReimbursementRepository) Save(ctx context.Context, r *domain.Reimbursement, expected int) error {
	err := connection(ctx, s.Pool).QueryRow(ctx, `UPDATE reimbursements SET status = $3, decided_by = $4, decided_at = $5, decision_reason = $6,
		destination_id = $7, bank_reference = $8, bank_reference_key = $9, paid_by = $10, paid_at = $11, version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $2 RETURNING version, updated_at`,
		r.ID, expected, r.Status, r.DecidedBy, r.DecidedAt, r.DecisionReason, r.DestinationID, r.BankReference, r.BankReferenceKey,
		r.PaidBy, r.PaidAt).Scan(&r.Version, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleState
	}
	if isUniqueViolation(err, "reimbursements_bank_reference_idx") {
		return ErrDuplicateBankReference
	}
	return err
}

// VerifiedDestination is the buyer's verified refund account on this
// order (the newest), with how screens show it.
func (s ReimbursementRepository) VerifiedDestination(ctx context.Context, orderID, buyerID string) (id, masked string, err error) {
	err = connection(ctx, s.Pool).QueryRow(ctx, `SELECT d.id::text, d.bank_code || ' ••••' || d.account_last4 FROM refund_destinations d
		JOIN payment_refunds f ON f.id = d.refund_id JOIN payment_intents p ON p.id = f.payment_intent_id
		WHERE f.order_id = $1 AND p.buyer_id = $2 AND d.status = 'verified' ORDER BY d.decided_at DESC NULLS LAST LIMIT 1`, orderID, buyerID).
		Scan(&id, &masked)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrDestinationNotFound
	}
	return id, masked, err
}

// BankReferenceUsed reports a bank reference already recorded for a manual
// refund transfer (a reimbursement's own duplicates are a unique index).
func (s ReimbursementRepository) BankReferenceUsed(ctx context.Context, key string) (bool, error) {
	var used bool
	err := connection(ctx, s.Pool).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM manual_refund_attempts WHERE bank_reference_key = $1)`, key).Scan(&used)
	return used, err
}

// List is the admin queue, newest first.
func (s ReimbursementRepository) List(ctx context.Context, status string, limit, offset int) ([]*domain.Reimbursement, error) {
	rows, err := connection(ctx, s.Pool).Query(ctx, `SELECT `+reimbursementColumns+reimbursementFrom+
		`WHERE ($1 = '' OR r.status = $1) ORDER BY r.created_at DESC, r.id LIMIT $2 OFFSET $3`, status, limit, offset)
	return collect(rows, err, scanReimbursement)
}
