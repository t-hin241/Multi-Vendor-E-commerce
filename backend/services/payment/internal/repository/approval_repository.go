package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/payment/internal/domain"
)

var (
	ErrApprovalNotFound = errors.New("repository: approval request not found")
	// ErrApprovalOpen: another draft or pending request exists for the target.
	ErrApprovalOpen = errors.New("repository: an open approval request exists for this target")
)

// ApprovalRepository stores maker-checker requests (AF-19).
type ApprovalRepository struct{ Pool *pgxpool.Pool }

const approvalColumns = `id::text, operation_kind, target_id, payload, payload_hash, snapshot, status, maker_id::text, maker_permission_version,
	reason, checker_id::text, checker_permission_version, decision_reason, version, expires_at, created_at, submitted_at, decided_at, execution_ref`

func scanApproval(row pgx.Row) (*domain.ApprovalRequest, error) {
	var a domain.ApprovalRequest
	err := row.Scan(&a.ID, &a.Kind, &a.TargetID, &a.Payload, &a.PayloadHash, &a.Snapshot, &a.Status, &a.MakerID, &a.MakerPermissionVersion,
		&a.Reason, &a.CheckerID, &a.CheckerPermissionVersion, &a.DecisionReason, &a.Version, &a.ExpiresAt, &a.CreatedAt, &a.SubmittedAt,
		&a.DecidedAt, &a.ExecutionRef)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrApprovalNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// Create stores a new draft.
func (r ApprovalRepository) Create(ctx context.Context, a *domain.ApprovalRequest) error {
	row := connection(ctx, r.Pool).QueryRow(ctx, `INSERT INTO approval_requests (operation_kind, target_id, payload, payload_hash, snapshot, status,
		maker_id, maker_permission_version, reason, expires_at) VALUES ($1, $2, $3, $4, $5, 'draft', $6, $7, $8, $9) RETURNING `+approvalColumns,
		a.Kind, a.TargetID, a.Payload, a.PayloadHash, a.Snapshot, a.MakerID, a.MakerPermissionVersion, a.Reason, a.ExpiresAt)
	created, err := scanApproval(row)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return ErrApprovalOpen
	}
	if err != nil {
		return err
	}
	*a = *created
	return nil
}

// Find reads one request; inside a transaction it is locked.
func (r ApprovalRepository) Find(ctx context.Context, id string) (*domain.ApprovalRequest, error) {
	q := `SELECT ` + approvalColumns + ` FROM approval_requests WHERE id = $1`
	if inTransaction(ctx) {
		q += ` FOR UPDATE`
	}
	return scanApproval(connection(ctx, r.Pool).QueryRow(ctx, q, id))
}

// List lists requests, newest first; status filters ("" = all).
func (r ApprovalRepository) List(ctx context.Context, status string, limit, offset int) ([]*domain.ApprovalRequest, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+approvalColumns+` FROM approval_requests
		WHERE ($1 = '' OR status = $1) ORDER BY created_at DESC, id LIMIT $2 OFFSET $3`, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.ApprovalRequest{}
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ApprovalChange is a state change of one request at an expected version.
type ApprovalChange struct {
	ID                       string
	ExpectedVersion          int64
	From                     domain.ApprovalStatus
	To                       domain.ApprovalStatus
	At                       time.Time
	CheckerID                *string
	CheckerPermissionVersion *int64
	DecisionReason           *string
	ExecutionRef             *string
}

// Transition moves a request from one status to the next, once.
func (r ApprovalRepository) Transition(ctx context.Context, c ApprovalChange) (*domain.ApprovalRequest, error) {
	row := connection(ctx, r.Pool).QueryRow(ctx, `UPDATE approval_requests SET status = $3, version = version + 1,
		submitted_at = CASE WHEN $3 = 'pending' THEN $4 ELSE submitted_at END,
		decided_at = CASE WHEN $3 IN ('approved', 'rejected') THEN $4 ELSE decided_at END,
		checker_id = COALESCE($5::uuid, checker_id), checker_permission_version = COALESCE($6, checker_permission_version),
		decision_reason = COALESCE($7, decision_reason), execution_ref = COALESCE($8, execution_ref)
		WHERE id = $1 AND version = $2 AND status = $9 RETURNING `+approvalColumns,
		c.ID, c.ExpectedVersion, c.To, c.At, c.CheckerID, c.CheckerPermissionVersion, c.DecisionReason, c.ExecutionRef, c.From)
	a, err := scanApproval(row)
	if errors.Is(err, ErrApprovalNotFound) {
		return nil, ErrStaleState
	}
	return a, err
}
