package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/middleware"
	"shopee/backend/services/payment/internal/domain"
)

var (
	ErrPayoutBatchNotFound = errors.New("repository: payout batch not found")
	ErrPayoutItemNotFound  = errors.New("repository: payout item not found")
)

const payoutItemColumns = `id, payout_batch_id, vendor_id, amount, COALESCE(currency, ''), COALESCE(destination_account_id::text, ''),
	COALESCE(destination_version, 0), destination_mask, status, evidence_reference, note, failure_reason, resolved_by, resolved_at, created_at`

// PayoutRepository stores manual payout batches and their items.
type PayoutRepository struct{ pool *pgxpool.Pool }

func NewPayoutRepository(pool *pgxpool.Pool) *PayoutRepository { return &PayoutRepository{pool: pool} }

func scanPayoutItem(row pgx.Row) (*domain.PayoutItem, error) {
	var i domain.PayoutItem
	err := row.Scan(&i.ID, &i.BatchID, &i.VendorID, &i.Amount, &i.Currency, &i.DestinationAccountID, &i.DestinationVersion, &i.DestinationMask,
		&i.Status, &i.EvidenceReference, &i.Note, &i.FailureReason, &i.ResolvedBy, &i.ResolvedAt, &i.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPayoutItemNotFound
	}
	return &i, err
}

// LockVendor serialises payout creation for one vendor, so two batches can
// never include the same ledger entries.
func (r *PayoutRepository) LockVendor(ctx context.Context, vendorID string) error {
	_, err := connection(ctx, r.pool).Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('payout:' || $1, 0))`, vendorID)
	return err
}

// CreateBatch creates a batch once per idempotency key; a replay returns the
// existing batch with created=false.
func (r *PayoutRepository) CreateBatch(ctx context.Context, key, currency, actor string) (*domain.PayoutBatch, bool, error) {
	var b domain.PayoutBatch
	err := connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO payout_batches (provider, idempotency_key, status, created_by, currency) VALUES ('manual', $1, 'pending', $2, $3)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id, idempotency_key, COALESCE(currency, ''), status, created_by, created_at`, key, actor, currency).
		Scan(&b.ID, &b.IdempotencyKey, &b.Currency, &b.Status, &b.CreatedBy, &b.CreatedAt)
	if err == nil {
		return &b, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	existing, err := r.FindBatchByKey(ctx, key)
	return existing, false, err
}

const payoutBatchColumns = `id, idempotency_key, COALESCE(currency, ''), status, created_by, created_at, COALESCE(total_amount, 0),
	approval_state, required_approvals, risk_reasons, first_approved_by, first_approved_at, second_approved_by, second_approved_at,
	decided_by, decision_reason, decided_at`

func scanPayoutBatch(row pgx.Row) (*domain.PayoutBatch, error) {
	var b domain.PayoutBatch
	a := &b.Approval
	err := row.Scan(&b.ID, &b.IdempotencyKey, &b.Currency, &b.Status, &b.CreatedBy, &b.CreatedAt, &b.TotalAmount,
		&a.State, &a.RequiredApprovals, &a.RiskReasons, &a.FirstApprovedBy, &a.FirstApprovedAt, &a.SecondApprovedBy, &a.SecondApprovedAt,
		&a.DecidedBy, &a.DecisionReason, &a.DecidedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPayoutBatchNotFound
	}
	return &b, err
}

func (r *PayoutRepository) findBatch(ctx context.Context, where string, arg string) (*domain.PayoutBatch, error) {
	b, err := scanPayoutBatch(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+payoutBatchColumns+` FROM payout_batches WHERE `+where, arg))
	if err != nil {
		return nil, err
	}
	items, err := r.items(ctx, `SELECT `+payoutItemColumns+` FROM payout_items WHERE payout_batch_id = $1 ORDER BY created_at, id`, b.ID)
	b.Items = items
	return b, err
}

// LockBatch reads a batch with its items, locking the batch row.
func (r *PayoutRepository) LockBatch(ctx context.Context, id string) (*domain.PayoutBatch, error) {
	return r.findBatch(ctx, "id = $1 FOR UPDATE", id)
}

// SetApprovalRequirement records a new batch's total and the approvals it
// needs (PW-027); the batch then waits for its first approval.
func (r *PayoutRepository) SetApprovalRequirement(ctx context.Context, batchID string, total int64, required int, reasons []string) error {
	_, err := connection(ctx, r.pool).Exec(ctx, `UPDATE payout_batches SET total_amount = $2, required_approvals = $3, risk_reasons = $4,
		approval_state = 'awaiting_approval', updated_at = now() WHERE id = $1 AND approval_state = 'not_required'`, batchID, total, required, reasons)
	return err
}

// SaveApproval stores the batch's approval record if it is still in state
// from (compare-and-set).
func (r *PayoutRepository) SaveApproval(ctx context.Context, b *domain.PayoutBatch, from string) error {
	a := b.Approval
	tag, err := connection(ctx, r.pool).Exec(ctx, `UPDATE payout_batches SET approval_state = $3, first_approved_by = $4, first_approved_at = $5,
		second_approved_by = $6, second_approved_at = $7, decided_by = $8, decision_reason = $9, decided_at = $10, updated_at = now()
		WHERE id = $1 AND approval_state = $2`, b.ID, from, a.State, a.FirstApprovedBy, a.FirstApprovedAt, a.SecondApprovedBy, a.SecondApprovedAt,
		a.DecidedBy, a.DecisionReason, a.DecidedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleState
	}
	return nil
}

// CancelPendingItems cancels a closed batch's untransferred items (their
// entries are unpaid again) and completes the batch.
func (r *PayoutRepository) CancelPendingItems(ctx context.Context, batchID, actor, reason string) (int64, error) {
	tag, err := connection(ctx, r.pool).Exec(ctx, `UPDATE payout_items SET status = 'cancelled', note = $3, resolved_by = $2, resolved_at = now(), updated_at = now()
		WHERE payout_batch_id = $1 AND status = 'pending'`, batchID, actor, reason)
	if err != nil {
		return 0, err
	}
	_, err = connection(ctx, r.pool).Exec(ctx, `UPDATE payout_batches SET status = 'completed', updated_at = now() WHERE id = $1 AND status = 'pending'`, batchID)
	return tag.RowsAffected(), err
}

// NewDestinations counts the batch's items paying a destination (account
// and version) that never received a succeeded payout.
func (r *PayoutRepository) NewDestinations(ctx context.Context, batchID string) (int, error) {
	var n int
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT count(*) FROM payout_items i WHERE i.payout_batch_id = $1
		AND NOT EXISTS (SELECT 1 FROM payout_items p WHERE p.status = 'succeeded' AND p.vendor_id = i.vendor_id
			AND p.destination_account_id = i.destination_account_id AND p.destination_version = i.destination_version)`, batchID).Scan(&n)
	return n, err
}

// ItemBatchApproval reads the approval of an item's batch, sharing the
// batch lock (taken before the item's) so an approval change cannot
// interleave with a transfer result.
func (r *PayoutRepository) ItemBatchApproval(ctx context.Context, itemID string) (domain.PayoutBatchApproval, error) {
	var a domain.PayoutBatchApproval
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT b.approval_state, b.required_approvals FROM payout_batches b
		JOIN payout_items i ON i.payout_batch_id = b.id WHERE i.id = $1 FOR SHARE OF b`, itemID).Scan(&a.State, &a.RequiredApprovals)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrPayoutItemNotFound
	}
	return a, err
}

func (r *PayoutRepository) FindBatch(ctx context.Context, id string) (*domain.PayoutBatch, error) {
	return r.findBatch(ctx, "id = $1", id)
}

func (r *PayoutRepository) FindBatchByKey(ctx context.Context, key string) (*domain.PayoutBatch, error) {
	return r.findBatch(ctx, "idempotency_key = $1", key)
}

func (r *PayoutRepository) items(ctx context.Context, query string, args ...any) ([]*domain.PayoutItem, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.PayoutItem{}
	for rows.Next() {
		it, err := scanPayoutItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// AddItem stores a vendor's payout item and links the entries it pays.
func (r *PayoutRepository) AddItem(ctx context.Context, item *domain.PayoutItem, entryIDs []string) error {
	err := connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO payout_items (payout_batch_id, vendor_id, amount, currency, destination_account_id, destination_version, destination_mask, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending') RETURNING id, status, created_at`,
		item.BatchID, item.VendorID, item.Amount, item.Currency, item.DestinationAccountID, item.DestinationVersion, item.DestinationMask).
		Scan(&item.ID, &item.Status, &item.CreatedAt)
	if err != nil {
		return err
	}
	for _, id := range entryIDs {
		if _, err := connection(ctx, r.pool).Exec(ctx, `INSERT INTO payout_item_entries (payout_item_id, entry_id) VALUES ($1, $2)`, item.ID, id); err != nil {
			return err
		}
	}
	return nil
}

// LockItem reads an item for update.
func (r *PayoutRepository) LockItem(ctx context.Context, id string) (*domain.PayoutItem, error) {
	return scanPayoutItem(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+payoutItemColumns+` FROM payout_items WHERE id = $1 FOR UPDATE`, id))
}

// SaveResolution persists an operator's result and completes the batch once
// every item is resolved.
func (r *PayoutRepository) SaveResolution(ctx context.Context, item *domain.PayoutItem) error {
	tag, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE payout_items SET status = $2, evidence_reference = $3, note = $4, failure_reason = $5, resolved_by = $6, resolved_at = $7, updated_at = now()
		WHERE id = $1 AND status = 'pending'`, item.ID, item.Status, item.EvidenceReference, item.Note, item.FailureReason, item.ResolvedBy, item.ResolvedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleState
	}
	_, err = connection(ctx, r.pool).Exec(ctx, `
		UPDATE payout_batches b SET status = 'completed', updated_at = now()
		WHERE b.id = $1 AND b.status = 'pending'
		  AND NOT EXISTS (SELECT 1 FROM payout_items i WHERE i.payout_batch_id = b.id AND i.status = 'pending')`, item.BatchID)
	return err
}

// ListBatches lists batches newest first.
func (r *PayoutRepository) ListBatches(ctx context.Context, limit, offset int) ([]*domain.PayoutBatch, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT `+payoutBatchColumns+`
		FROM payout_batches ORDER BY created_at DESC, id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.PayoutBatch{}
	for rows.Next() {
		b, err := scanPayoutBatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// AuditRepository records admin reconciliation and settlement actions.
type AuditRepository struct{ pool *pgxpool.Pool }

func NewAuditRepository(pool *pgxpool.Pool) *AuditRepository { return &AuditRepository{pool: pool} }

// Record appends an audit row; a reason composed from several parts is
// clipped to the column's 500 characters rather than failing the action.
func (r *AuditRepository) Record(ctx context.Context, actor, action, targetType, targetID, reason string) error {
	if runes := []rune(reason); len(runes) > 500 {
		reason = string(runes[:499]) + "…"
	}
	_, err := connection(ctx, r.pool).Exec(ctx, `
		INSERT INTO payment_admin_audit (actor_id, action, target_type, target_id, reason, request_id) VALUES ($1, $2, $3, $4, $5, $6)`,
		actor, action, targetType, targetID, reason, middleware.CorrelationID(ctx))
	return err
}

// AuditEntry is one recorded admin action.
type AuditEntry struct {
	ActorID    string    `json:"actor_id"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
}

// ForTarget lists the actions on one target, oldest first.
func (r *AuditRepository) ForTarget(ctx context.Context, targetType, targetID string) ([]AuditEntry, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT actor_id::text, action, target_type, target_id, reason, created_at
		FROM payment_admin_audit WHERE target_type = $1 AND target_id = $2 ORDER BY created_at`, targetType, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var a AuditEntry
		if err := rows.Scan(&a.ActorID, &a.Action, &a.TargetType, &a.TargetID, &a.Reason, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
