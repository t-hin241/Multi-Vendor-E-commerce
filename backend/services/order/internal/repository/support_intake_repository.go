package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/order/internal/domain"
)

var ErrIntakeNotFound = errors.New("repository: support intake not found")

// SupportIntakeRepository stores buyers' requests without an order id.
type SupportIntakeRepository struct{ Pool *pgxpool.Pool }

const intakeColumns = `id, buyer_id, reference_kind, reference, message, status, linked_case_id, handled_by, handled_at, close_reason,
	idempotency_key, request_hash, version, created_at, updated_at`

func scanIntake(row pgx.Row) (*domain.SupportIntake, error) {
	var in domain.SupportIntake
	err := row.Scan(&in.ID, &in.BuyerID, &in.ReferenceKind, &in.Reference, &in.Message, &in.Status, &in.LinkedCaseID, &in.HandledBy,
		&in.HandledAt, &in.CloseReason, &in.IdempotencyKey, &in.RequestHash, &in.Version, &in.CreatedAt, &in.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrIntakeNotFound
	}
	return &in, err
}

func collectIntakes(rows pgx.Rows, err error) ([]*domain.SupportIntake, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.SupportIntake{}
	for rows.Next() {
		in, err := scanIntake(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// Create inserts an intake; a second open one for the same reference or a
// reused idempotency key is refused by its unique index.
func (r SupportIntakeRepository) Create(ctx context.Context, in *domain.SupportIntake) error {
	err := connection(ctx, r.Pool).QueryRow(ctx, `INSERT INTO support_intakes (buyer_id, reference_kind, reference, message, idempotency_key, request_hash)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, status, version, created_at, updated_at`,
		in.BuyerID, in.ReferenceKind, in.Reference, in.Message, in.IdempotencyKey, in.RequestHash).
		Scan(&in.ID, &in.Status, &in.Version, &in.CreatedAt, &in.UpdatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		if pgErr.ConstraintName == "support_intakes_idempotency_key" {
			return ErrSupportKeyTaken
		}
		return domain.IntakeAlreadyOpen()
	}
	return err
}

// FindByID reads an intake; inside a transaction it is locked.
func (r SupportIntakeRepository) FindByID(ctx context.Context, id string) (*domain.SupportIntake, error) {
	lock := ""
	if InTransaction(ctx) {
		lock = " FOR UPDATE"
	}
	return scanIntake(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+intakeColumns+` FROM support_intakes WHERE id = $1`+lock, id))
}

// FindByKey is the buyer's intake created with key, or nil.
func (r SupportIntakeRepository) FindByKey(ctx context.Context, buyerID, key string) (*domain.SupportIntake, error) {
	in, err := scanIntake(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+intakeColumns+` FROM support_intakes
		WHERE buyer_id = $1 AND idempotency_key = $2`, buyerID, key))
	if errors.Is(err, ErrIntakeNotFound) {
		return nil, nil
	}
	return in, err
}

func (r SupportIntakeRepository) CountOpen(ctx context.Context, buyerID string) (int, error) {
	var n int
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT count(*) FROM support_intakes WHERE buyer_id = $1 AND status = 'open'`, buyerID).Scan(&n)
	return n, err
}

func (r SupportIntakeRepository) ListByBuyer(ctx context.Context, buyerID string, limit int) ([]*domain.SupportIntake, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+intakeColumns+` FROM support_intakes WHERE buyer_id = $1
		ORDER BY created_at DESC, id LIMIT $2`, buyerID, limit)
	return collectIntakes(rows, err)
}

// List is the admin queue, oldest open first; status "" lists all.
func (r SupportIntakeRepository) List(ctx context.Context, status string, limit, offset int) ([]*domain.SupportIntake, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+intakeColumns+` FROM support_intakes WHERE ($1 = '' OR status = $1)
		ORDER BY created_at, id LIMIT $2 OFFSET $3`, status, limit, offset)
	return collectIntakes(rows, err)
}

// Save writes a handled intake with compare-and-set on its version.
func (r SupportIntakeRepository) Save(ctx context.Context, in *domain.SupportIntake) error {
	err := connection(ctx, r.Pool).QueryRow(ctx, `UPDATE support_intakes SET status = $3, linked_case_id = $4, handled_by = $5, handled_at = $6,
		close_reason = $7, version = version + 1, updated_at = now() WHERE id = $1 AND version = $2 RETURNING version, updated_at`,
		in.ID, in.Version, in.Status, in.LinkedCaseID, in.HandledBy, in.HandledAt, in.CloseReason).Scan(&in.Version, &in.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleState
	}
	return err
}
