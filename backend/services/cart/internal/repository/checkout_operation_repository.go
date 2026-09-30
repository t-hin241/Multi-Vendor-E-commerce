package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/cart/internal/domain"
)

// CheckoutOperationRepository stores checkout snapshots and their consume
// receipts. Callers serialize access per buyer through the cart row lock.
type CheckoutOperationRepository struct {
	pool *pgxpool.Pool
}

func NewCheckoutOperationRepository(pool *pgxpool.Pool) *CheckoutOperationRepository {
	return &CheckoutOperationRepository{pool: pool}
}

// Find returns the operation, or nil if it does not exist.
func (r *CheckoutOperationRepository) Find(ctx context.Context, operationID string) (*domain.CheckoutOperation, error) {
	ctx, cancel := statementContext(ctx)
	defer cancel()

	var (
		op      domain.CheckoutOperation
		lines   []byte
		receipt []byte
	)
	err := connection(ctx, r.pool).QueryRow(ctx, `
		SELECT operation_id, buyer_id, cart_id, cart_version, lines, created_at, consume_hash, receipt
		FROM cart_checkout_operations WHERE operation_id = $1`, operationID,
	).Scan(&op.OperationID, &op.BuyerID, &op.CartID, &op.CartVersion, &lines, &op.CreatedAt, &op.ConsumeHash, &receipt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(lines, &op.Lines); err != nil {
		return nil, fmt.Errorf("decode snapshot lines of operation %s: %w", operationID, err)
	}
	if receipt != nil {
		op.Receipt = &domain.ConsumeReceipt{}
		if err := json.Unmarshal(receipt, op.Receipt); err != nil {
			return nil, fmt.Errorf("decode consume receipt of operation %s: %w", operationID, err)
		}
	}
	return &op, nil
}

func (r *CheckoutOperationRepository) Insert(ctx context.Context, op *domain.CheckoutOperation) error {
	lines, err := json.Marshal(op.Lines)
	if err != nil {
		return err
	}
	ctx, cancel := statementContext(ctx)
	defer cancel()
	return connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO cart_checkout_operations (operation_id, buyer_id, cart_id, cart_version, lines)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at`,
		op.OperationID, op.BuyerID, op.CartID, op.CartVersion, lines,
	).Scan(&op.CreatedAt)
}

// MarkConsumed stores the receipt. It only succeeds once per operation.
func (r *CheckoutOperationRepository) MarkConsumed(ctx context.Context, operationID, hash string, receipt *domain.ConsumeReceipt) error {
	body, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	ctx, cancel := statementContext(ctx)
	defer cancel()
	tag, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE cart_checkout_operations SET consume_hash = $2, receipt = $3, consumed_at = $4
		WHERE operation_id = $1 AND consumed_at IS NULL`,
		operationID, hash, body, receipt.ConsumedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("checkout operation %s was already consumed", operationID)
	}
	return nil
}
