package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/casesla"
	"shopee/backend/services/payment/internal/domain"
)

var (
	ErrDestinationNotFound = errors.New("repository: refund destination not found")
	ErrAttemptNotFound     = errors.New("repository: manual refund attempt not found")
	ErrEvidenceNotFound    = errors.New("repository: refund evidence not found")
	// ErrDuplicateBankReference: the reference is already recorded for
	// another transfer from the same source account.
	ErrDuplicateBankReference = errors.New("repository: bank reference already recorded")
	// ErrAttemptActiveExists: another active attempt holds the refund.
	ErrAttemptActiveExists = errors.New("repository: an active manual refund attempt exists")
)

// ManualRefundRepository stores AF-06 destinations, attempts and evidence.
// Writes run in the use case's transaction (Transactions.Run); callers
// lock the refund first so every manual step on one refund is serialised.
type ManualRefundRepository struct{ Pool *pgxpool.Pool }

const destinationColumns = `id, refund_id, version, bank_code, account_last4, ciphertext, key_version, status, submitted_by, submitted_at,
	decided_by, decided_at, decision_reason`

func scanDestination(row pgx.Row) (*domain.RefundDestination, error) {
	var d domain.RefundDestination
	err := row.Scan(&d.ID, &d.RefundID, &d.Version, &d.BankCode, &d.AccountLast4, &d.Ciphertext, &d.KeyVersion, &d.Status, &d.SubmittedBy, &d.SubmittedAt,
		&d.DecidedBy, &d.DecidedAt, &d.DecisionReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDestinationNotFound
	}
	return &d, err
}

const attemptColumns = `id, refund_id, destination_id, destination_version, amount, currency, stage, version, prepared_by, prepare_reason,
	claimed_by, claimed_at, lease_expires_at, source_account, bank_reference, bank_reference_key, executed_at, submitted_by, submitted_at,
	decided_by, decided_at, decision_reason, created_at, updated_at`

func scanAttempt(row pgx.Row) (*domain.ManualRefundAttempt, error) {
	var a domain.ManualRefundAttempt
	err := row.Scan(&a.ID, &a.RefundID, &a.DestinationID, &a.DestinationVersion, &a.Amount, &a.Currency, &a.Stage, &a.Version, &a.PreparedBy, &a.PrepareReason,
		&a.ClaimedBy, &a.ClaimedAt, &a.LeaseExpiresAt, &a.SourceAccount, &a.BankReference, &a.BankReferenceKey, &a.ExecutedAt, &a.SubmittedBy, &a.SubmittedAt,
		&a.DecidedBy, &a.DecidedAt, &a.DecisionReason, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAttemptNotFound
	}
	return &a, err
}

const evidenceColumns = `id, attempt_id, object_key, content_type, size_bytes, sha256, uploaded_by, state, created_at`

func scanEvidence(row pgx.Row) (*domain.RefundEvidence, error) {
	var e domain.RefundEvidence
	err := row.Scan(&e.ID, &e.AttemptID, &e.ObjectKey, &e.ContentType, &e.SizeBytes, &e.SHA256, &e.UploadedBy, &e.State, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrEvidenceNotFound
	}
	return &e, err
}

func collect[T any](rows pgx.Rows, err error, scan func(pgx.Row) (*T, error)) ([]*T, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// prefixed qualifies a column list with a table alias.
func prefixed(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// LockRefund locks the refund row for the rest of the transaction.
func (r ManualRefundRepository) LockRefund(ctx context.Context, id string) (*domain.Refund, error) {
	return scanRefund(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+refundColumns+` FROM payment_refunds WHERE id = $1 FOR UPDATE`, id))
}

// RefundBuyer is the buyer of the capture a refund draws on.
func (r ManualRefundRepository) RefundBuyer(ctx context.Context, refundID string) (string, error) {
	var buyer string
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT p.buyer_id FROM payment_refunds f JOIN payment_intents p ON p.id = f.payment_intent_id WHERE f.id = $1`, refundID).Scan(&buyer)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrRefundNotFound
	}
	return buyer, err
}

// BuyerRefunds lists a buyer's refunds, optionally of one order.
func (r ManualRefundRepository) BuyerRefunds(ctx context.Context, buyerID string, orderID *string) ([]*domain.Refund, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+prefixed("f", refundColumns)+` FROM payment_refunds f JOIN payment_intents p ON p.id = f.payment_intent_id
		WHERE p.buyer_id = $1 AND ($2::uuid IS NULL OR f.order_id = $2) ORDER BY f.created_at DESC LIMIT 50`, buyerID, orderID)
	return collect(rows, err, scanRefund)
}

// CurrentDestination is the destination in use (pending or verified).
func (r ManualRefundRepository) CurrentDestination(ctx context.Context, refundID string) (*domain.RefundDestination, error) {
	return scanDestination(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+destinationColumns+` FROM refund_destinations
		WHERE refund_id = $1 AND status IN ('pending_verification', 'verified')`, refundID))
}

// LatestDestination is the newest version whatever its status.
func (r ManualRefundRepository) LatestDestination(ctx context.Context, refundID string) (*domain.RefundDestination, error) {
	return scanDestination(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+destinationColumns+` FROM refund_destinations
		WHERE refund_id = $1 ORDER BY version DESC LIMIT 1`, refundID))
}

func (r ManualRefundRepository) Destinations(ctx context.Context, refundID string) ([]*domain.RefundDestination, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+destinationColumns+` FROM refund_destinations WHERE refund_id = $1 ORDER BY version DESC`, refundID)
	return collect(rows, err, scanDestination)
}

// AddDestination supersedes the destination in use and stores d as the
// next version (d.Version is set).
func (r ManualRefundRepository) AddDestination(ctx context.Context, d *domain.RefundDestination) error {
	q := connection(ctx, r.Pool)
	if _, err := q.Exec(ctx, `UPDATE refund_destinations SET status = 'superseded'
		WHERE refund_id = $1 AND status IN ('pending_verification', 'verified')`, d.RefundID); err != nil {
		return err
	}
	if err := q.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM refund_destinations WHERE refund_id = $1`, d.RefundID).Scan(&d.Version); err != nil {
		return err
	}
	if err := q.QueryRow(ctx, `INSERT INTO refund_destinations (refund_id, version, bank_code, account_last4, ciphertext, key_version, status, submitted_by, submitted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		d.RefundID, d.Version, d.BankCode, d.AccountLast4, d.Ciphertext, d.KeyVersion, d.Status, d.SubmittedBy, d.SubmittedAt).Scan(&d.ID); err != nil {
		return err
	}
	return r.SyncSLA(ctx, d.RefundID, d.SubmittedAt)
}

// DecideDestination records a verification decision on a pending version.
func (r ManualRefundRepository) DecideDestination(ctx context.Context, d *domain.RefundDestination) error {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE refund_destinations SET status = $2, decided_by = $3, decided_at = $4, decision_reason = $5
		WHERE id = $1 AND status = 'pending_verification'`, d.ID, d.Status, d.DecidedBy, d.DecidedAt, d.DecisionReason)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrStaleState
	}
	if err != nil {
		return err
	}
	at := time.Now().UTC()
	if d.DecidedAt != nil {
		at = *d.DecidedAt
	}
	return r.SyncSLA(ctx, d.RefundID, at)
}

// ActiveAttempt is the refund's ready/executing/submitted/unknown attempt.
func (r ManualRefundRepository) ActiveAttempt(ctx context.Context, refundID string) (*domain.ManualRefundAttempt, error) {
	return scanAttempt(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+attemptColumns+` FROM manual_refund_attempts
		WHERE refund_id = $1 AND stage IN ('ready', 'executing', 'submitted', 'unknown')`, refundID))
}

// HasActiveAttempt reports an active attempt without reading it.
func (r ManualRefundRepository) HasActiveAttempt(ctx context.Context, refundID string) (bool, error) {
	var exists bool
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM manual_refund_attempts
		WHERE refund_id = $1 AND stage IN ('ready', 'executing', 'submitted', 'unknown'))`, refundID).Scan(&exists)
	return exists, err
}

// FindAttempt reads an attempt; inside a transaction it is locked.
func (r ManualRefundRepository) FindAttempt(ctx context.Context, id string) (*domain.ManualRefundAttempt, error) {
	lock := ""
	if InTransaction(ctx) {
		lock = " FOR UPDATE"
	}
	return scanAttempt(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+attemptColumns+` FROM manual_refund_attempts WHERE id = $1`+lock, id))
}

func (r ManualRefundRepository) Attempts(ctx context.Context, refundID string) ([]*domain.ManualRefundAttempt, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+attemptColumns+` FROM manual_refund_attempts WHERE refund_id = $1 ORDER BY created_at DESC`, refundID)
	return collect(rows, err, scanAttempt)
}

func (r ManualRefundRepository) CreateAttempt(ctx context.Context, a *domain.ManualRefundAttempt) error {
	err := connection(ctx, r.Pool).QueryRow(ctx, `INSERT INTO manual_refund_attempts
		(refund_id, destination_id, destination_version, amount, currency, stage, prepared_by, prepare_reason, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'ready', $6, $7, $8, $8) RETURNING id, stage, version`,
		a.RefundID, a.DestinationID, a.DestinationVersion, a.Amount, a.Currency, a.PreparedBy, a.PrepareReason, a.CreatedAt).Scan(&a.ID, &a.Stage, &a.Version)
	if isUniqueViolation(err, "manual_refund_attempts_active_idx") {
		return ErrAttemptActiveExists
	}
	if err != nil {
		return err
	}
	a.UpdatedAt = a.CreatedAt
	return r.SyncSLA(ctx, a.RefundID, a.CreatedAt)
}

// SaveAttempt writes an attempt's new state if it still has version
// expected, and bumps the version.
func (r ManualRefundRepository) SaveAttempt(ctx context.Context, a *domain.ManualRefundAttempt, expected int, at time.Time) error {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE manual_refund_attempts SET stage = $3, version = version + 1,
		claimed_by = $4, claimed_at = $5, lease_expires_at = $6, source_account = $7, bank_reference = $8, bank_reference_key = $9, executed_at = $10,
		submitted_by = $11, submitted_at = $12, decided_by = $13, decided_at = $14, decision_reason = $15, updated_at = $16
		WHERE id = $1 AND version = $2`,
		a.ID, expected, a.Stage, a.ClaimedBy, a.ClaimedAt, a.LeaseExpiresAt, a.SourceAccount, a.BankReference, a.BankReferenceKey, a.ExecutedAt,
		a.SubmittedBy, a.SubmittedAt, a.DecidedBy, a.DecidedAt, a.DecisionReason, at)
	if isUniqueViolation(err, "manual_refund_attempts_reference_idx") {
		return ErrDuplicateBankReference
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleState
	}
	a.Version, a.UpdatedAt = expected+1, at
	return r.SyncSLA(ctx, a.RefundID, at)
}

// SyncSLA (PW-017) moves the refund's deadline to its manual workflow
// stage, entered at `at`, in the caller's transaction (every destination
// and attempt write calls it).
func (r ManualRefundRepository) SyncSLA(ctx context.Context, refundID string, at time.Time) error {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	if !ok {
		return errors.New("refund deadline sync requires a transaction")
	}
	refund, err := scanRefund(tx.QueryRow(ctx, `SELECT `+refundColumns+` FROM payment_refunds WHERE id = $1`, refundID))
	if err != nil {
		return err
	}
	dest, err := r.LatestDestination(ctx, refundID)
	if errors.Is(err, ErrDestinationNotFound) {
		dest = nil
	} else if err != nil {
		return err
	}
	attempt, err := r.ActiveAttempt(ctx, refundID)
	if errors.Is(err, ErrAttemptNotFound) {
		attempt = nil
	} else if err != nil {
		return err
	}
	_, err = casesla.Sync(ctx, tx, domain.ManualSLAStage(*refund, dest, attempt, at.UTC()))
	return err
}

// RestageLegacySLA moves open refunds still on the pre-workflow deadline
// (awaiting_refund_receipt) to their manual stage, once the workflow is on.
func (r ManualRefundRepository) RestageLegacySLA(ctx context.Context, limit int) (int, error) {
	rows, err := r.Pool.Query(ctx, `SELECT resource_id FROM case_sla_work_items
		WHERE resource_type = 'refund' AND active AND payload->>'stage' = 'awaiting_refund_receipt' ORDER BY created_at LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := (Transactions{Pool: r.Pool}).Run(ctx, func(ctx context.Context) error {
			if _, err := r.LockRefund(ctx, id); err != nil {
				return err
			}
			return r.SyncSLA(ctx, id, time.Now().UTC())
		}); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// ExpiredClaims lists executing attempts whose lease ran out.
func (r ManualRefundRepository) ExpiredClaims(ctx context.Context, now time.Time, limit int) ([]*domain.ManualRefundAttempt, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+attemptColumns+` FROM manual_refund_attempts
		WHERE stage = 'executing' AND lease_expires_at <= $1 ORDER BY lease_expires_at LIMIT $2`, now, limit)
	return collect(rows, err, scanAttempt)
}

// AddEvidence stores the metadata of an evidence file before its upload.
func (r ManualRefundRepository) AddEvidence(ctx context.Context, e *domain.RefundEvidence) error {
	return connection(ctx, r.Pool).QueryRow(ctx, `INSERT INTO refund_evidence (id, attempt_id, object_key, content_type, size_bytes, sha256, uploaded_by, state, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`, e.ID, e.AttemptID, e.ObjectKey, e.ContentType, e.SizeBytes, e.SHA256, e.UploadedBy, e.State, e.CreatedAt).Scan(&e.ID)
}

// SetEvidenceState moves an evidence row from one state to another.
func (r ManualRefundRepository) SetEvidenceState(ctx context.Context, id, from, to string) error {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE refund_evidence SET state = $3 WHERE id = $1 AND state = $2`, id, from, to)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrStaleState
	}
	return err
}

// AttachEvidence attaches uploaded files of this attempt; it reports how
// many matched.
func (r ManualRefundRepository) AttachEvidence(ctx context.Context, attemptID string, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE refund_evidence SET state = 'attached'
		WHERE attempt_id = $1 AND id = ANY($2::uuid[]) AND state = 'uploaded'`, attemptID, ids)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (r ManualRefundRepository) Evidence(ctx context.Context, attemptID string) ([]*domain.RefundEvidence, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+evidenceColumns+` FROM refund_evidence
		WHERE attempt_id = $1 AND state IN ('uploaded', 'attached') ORDER BY created_at`, attemptID)
	return collect(rows, err, scanEvidence)
}

func (r ManualRefundRepository) FindEvidence(ctx context.Context, id string) (*domain.RefundEvidence, error) {
	return scanEvidence(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+evidenceColumns+` FROM refund_evidence WHERE id = $1`, id))
}

// OrphanEvidence lists files never attached to a submission.
func (r ManualRefundRepository) OrphanEvidence(ctx context.Context, before time.Time, limit int) ([]*domain.RefundEvidence, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+evidenceColumns+` FROM refund_evidence
		WHERE state IN ('uploading', 'uploaded') AND created_at < $1 ORDER BY created_at LIMIT $2`, before, limit)
	return collect(rows, err, scanEvidence)
}

// ManualRefundCounts feed the operator report and alerts.
type ManualRefundCounts struct {
	Unknown              int64
	SubmittedOverdue     int64
	DestinationsOverdue  int64
	ExecutingOverdue     int64
	ReadyWithoutClaimant int64
}

// Counts reports attempts that need someone: unknown ones, submissions and
// destinations waiting longer than overdue, executing past the lease.
func (r ManualRefundRepository) Counts(ctx context.Context, now time.Time, overdue time.Duration) (ManualRefundCounts, error) {
	var c ManualRefundCounts
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT
		(SELECT count(*) FROM manual_refund_attempts WHERE stage = 'unknown'),
		(SELECT count(*) FROM manual_refund_attempts WHERE stage = 'submitted' AND submitted_at < $2),
		(SELECT count(*) FROM refund_destinations WHERE status = 'pending_verification' AND submitted_at < $2),
		(SELECT count(*) FROM manual_refund_attempts WHERE stage = 'executing' AND lease_expires_at <= $1),
		(SELECT count(*) FROM manual_refund_attempts WHERE stage = 'ready' AND created_at < $2)`,
		now, now.Add(-overdue)).Scan(&c.Unknown, &c.SubmittedOverdue, &c.DestinationsOverdue, &c.ExecutingOverdue, &c.ReadyWithoutClaimant)
	return c, err
}
