package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/casesla"
	"shopee/backend/services/order/internal/domain"
)

var ErrCancellationNotFound = errors.New("repository: cancellation request not found")

// CancellationRepository stores AF-03 requests, their timeline and the
// vendor order's handover claim. Writes run under the order lock the use
// case holds (withOrder), so a request and a handover claim are ordered.
type CancellationRepository struct{ Pool *pgxpool.Pool }

const cancellationColumns = `id, order_id, vendor_order_id, vendor_id, buyer_id, origin, requested_by, reason_code, reason, status, policy_version,
	hold_id, hold_status, hold_note, stop_result, restock, inventory_recovery_ref, refund_id, decided_by, decided_at, decision_reason,
	review_reason, resolved_at, idempotency_key, request_hash, version, created_at, updated_at,
	(SELECT due_at FROM case_sla_work_items w WHERE w.resource_type = 'cancellation' AND w.resource_id = cancellation_requests.id AND w.active),
	COALESCE((SELECT payload->>'waiting_on' FROM case_sla_work_items w WHERE w.resource_type = 'cancellation'
		AND w.resource_id = cancellation_requests.id AND w.active), '')`

func scanCancellation(row pgx.Row) (*domain.CancellationRequest, error) {
	var c domain.CancellationRequest
	err := row.Scan(&c.ID, &c.OrderID, &c.VendorOrderID, &c.VendorID, &c.BuyerID, &c.Origin, &c.RequestedBy, &c.ReasonCode, &c.Reason, &c.Status,
		&c.PolicyVersion, &c.HoldID, &c.HoldStatus, &c.HoldNote, &c.StopResult, &c.Restock, &c.RecoveryRef, &c.RefundID, &c.DecidedBy, &c.DecidedAt,
		&c.DecisionReason, &c.ReviewReason, &c.ResolvedAt, &c.IdempotencyKey, &c.RequestHash, &c.Version, &c.CreatedAt, &c.UpdatedAt,
		&c.ActionDueAt, &c.WaitingOn)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCancellationNotFound
	}
	return &c, err
}

func collectCancellations(rows pgx.Rows, err error) ([]*domain.CancellationRequest, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.CancellationRequest{}
	for rows.Next() {
		c, err := scanCancellation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r CancellationRepository) syncSLA(ctx context.Context, c *domain.CancellationRequest) error {
	tx, _ := ctx.Value(transactionKey{}).(pgx.Tx)
	i, err := casesla.Sync(ctx, tx, c.SLAStage())
	if i != nil && i.Active {
		due := i.EffectiveDueAt()
		c.ActionDueAt, c.WaitingOn = &due, i.WaitingOn
	} else {
		c.ActionDueAt, c.WaitingOn = nil, ""
	}
	return err
}

// Create inserts a request; a second open one for the vendor order or a
// reused idempotency key is refused by its unique index.
func (r CancellationRepository) Create(ctx context.Context, c *domain.CancellationRequest) error {
	if !InTransaction(ctx) {
		return (Transactions{Pool: r.Pool}).Run(ctx, func(ctx context.Context) error { return r.Create(ctx, c) })
	}
	err := connection(ctx, r.Pool).QueryRow(ctx, `INSERT INTO cancellation_requests (order_id, vendor_order_id, vendor_id, buyer_id, origin,
		requested_by, reason_code, reason, status, policy_version, hold_id, hold_status, idempotency_key, request_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) RETURNING id, version, created_at, updated_at`,
		c.OrderID, c.VendorOrderID, c.VendorID, c.BuyerID, c.Origin, c.RequestedBy, c.ReasonCode, c.Reason, c.Status, c.PolicyVersion,
		c.HoldID, c.HoldStatus, c.IdempotencyKey, c.RequestHash).Scan(&c.ID, &c.Version, &c.CreatedAt, &c.UpdatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		if pgErr.ConstraintName == "cancellation_requests_idempotency_idx" {
			return ErrSupportKeyTaken
		}
		return domain.CancellationExists()
	}
	if err != nil {
		return err
	}
	return r.syncSLA(ctx, c)
}

// FindByID reads a request; inside a transaction it is locked.
func (r CancellationRepository) FindByID(ctx context.Context, id string) (*domain.CancellationRequest, error) {
	lock := ""
	if InTransaction(ctx) {
		lock = " FOR UPDATE"
	}
	return scanCancellation(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+cancellationColumns+` FROM cancellation_requests WHERE id = $1`+lock, id))
}

// FindByKey is the requester's request created with key, or nil.
func (r CancellationRepository) FindByKey(ctx context.Context, requesterID, key string) (*domain.CancellationRequest, error) {
	c, err := scanCancellation(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+cancellationColumns+` FROM cancellation_requests
		WHERE requested_by = $1 AND idempotency_key = $2`, requesterID, key))
	if errors.Is(err, ErrCancellationNotFound) {
		return nil, nil
	}
	return c, err
}

// FindOpen is the vendor order's open request, or nil.
func (r CancellationRepository) FindOpen(ctx context.Context, vendorOrderID string) (*domain.CancellationRequest, error) {
	c, err := scanCancellation(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+cancellationColumns+` FROM cancellation_requests
		WHERE vendor_order_id = $1 AND status NOT IN ('rejected', 'resolved')`, vendorOrderID))
	if errors.Is(err, ErrCancellationNotFound) {
		return nil, nil
	}
	return c, err
}

// FindByRefund is the request a refund pays, or nil.
func (r CancellationRepository) FindByRefund(ctx context.Context, refundID string) (*domain.CancellationRequest, error) {
	c, err := scanCancellation(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+cancellationColumns+` FROM cancellation_requests
		WHERE refund_id = $1`, refundID))
	if errors.Is(err, ErrCancellationNotFound) {
		return nil, nil
	}
	return c, err
}

func (r CancellationRepository) ListByOrder(ctx context.Context, orderID string) ([]*domain.CancellationRequest, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+cancellationColumns+` FROM cancellation_requests WHERE order_id = $1
		ORDER BY created_at DESC, id`, orderID)
	return collectCancellations(rows, err)
}

func (r CancellationRepository) ListForVendor(ctx context.Context, vendorID, status string, limit, offset int) ([]*domain.CancellationRequest, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+cancellationColumns+` FROM cancellation_requests
		WHERE vendor_id = $1 AND ($2 = '' OR status = $2) ORDER BY created_at DESC, id LIMIT $3 OFFSET $4`, vendorID, status, limit, offset)
	return collectCancellations(rows, err)
}

// List is the admin queue: "open" lists every request not yet final.
func (r CancellationRepository) List(ctx context.Context, status string, limit, offset int) ([]*domain.CancellationRequest, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+cancellationColumns+` FROM cancellation_requests
		WHERE ($1 = '' OR status = $1 OR ($1 = 'open' AND status NOT IN ('rejected', 'resolved')))
		ORDER BY created_at, id LIMIT $2 OFFSET $3`, status, limit, offset)
	return collectCancellations(rows, err)
}

// Save writes a request's state with compare-and-set on its version.
func (r CancellationRepository) Save(ctx context.Context, c *domain.CancellationRequest) error {
	err := connection(ctx, r.Pool).QueryRow(ctx, `UPDATE cancellation_requests SET status = $3, stop_result = $4, restock = $5,
		inventory_recovery_ref = $6, refund_id = $7, decided_by = $8, decided_at = $9, decision_reason = $10, review_reason = $11,
		resolved_at = $12, version = version + 1, updated_at = now() WHERE id = $1 AND version = $2 RETURNING version, updated_at`,
		c.ID, c.Version, c.Status, c.StopResult, c.Restock, c.RecoveryRef, c.RefundID, c.DecidedBy, c.DecidedAt, c.DecisionReason,
		c.ReviewReason, c.ResolvedAt).Scan(&c.Version, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleState
	}
	if err != nil {
		return err
	}
	return r.syncSLA(ctx, c)
}

// SetHold moves the request's hold from one of from to to, without a
// version bump (Payment's answer never makes an admin's view stale).
// from empty: give a request without a hold its hold id (preparing).
func (r CancellationRepository) SetHold(ctx context.Context, id string, holdID *string, from []string, to string, note *string) (bool, error) {
	var tag pgconn.CommandTag
	var err error
	if holdID != nil {
		tag, err = connection(ctx, r.Pool).Exec(ctx, `UPDATE cancellation_requests SET hold_id = $2, hold_status = $3, hold_note = NULL
			WHERE id = $1 AND hold_id IS NULL`, id, *holdID, to)
	} else {
		tag, err = connection(ctx, r.Pool).Exec(ctx, `UPDATE cancellation_requests SET hold_status = $3, hold_note = $4
			WHERE id = $1 AND hold_status = ANY($2)`, id, from, to, note)
	}
	return err == nil && tag.RowsAffected() == 1, err
}

func (r CancellationRepository) AddEvent(ctx context.Context, e *domain.CancellationEvent) error {
	return connection(ctx, r.Pool).QueryRow(ctx, `INSERT INTO cancellation_request_history (request_id, actor_id, actor_role, action,
		from_status, to_status, note) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at`,
		e.RequestID, e.ActorID, e.ActorRole, e.Action, e.FromStatus, e.ToStatus, e.Note).Scan(&e.ID, &e.CreatedAt)
}

func (r CancellationRepository) ListEvents(ctx context.Context, requestID string) ([]*domain.CancellationEvent, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT id, request_id, actor_id, actor_role, action, from_status, to_status, note, created_at
		FROM cancellation_request_history WHERE request_id = $1 ORDER BY created_at, id`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.CancellationEvent{}
	for rows.Next() {
		var e domain.CancellationEvent
		if err := rows.Scan(&e.ID, &e.RequestID, &e.ActorID, &e.ActorRole, &e.Action, &e.FromStatus, &e.ToStatus, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// Handover is the fulfillment grant Shipment claimed, if any.
func (r CancellationRepository) Handover(ctx context.Context, vendorOrderID string) (claimedAt *time.Time, shipmentID *string, err error) {
	err = connection(ctx, r.Pool).QueryRow(ctx, `SELECT handover_claimed_at, handover_shipment_id FROM vendor_orders WHERE id = $1`, vendorOrderID).
		Scan(&claimedAt, &shipmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrVendorOrderNotFound
	}
	return claimedAt, shipmentID, err
}

// ClaimHandover records the grant once; a later claim by the same
// shipment keeps the first time.
func (r CancellationRepository) ClaimHandover(ctx context.Context, vendorOrderID, shipmentID string) error {
	_, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE vendor_orders SET handover_claimed_at = COALESCE(handover_claimed_at, now()),
		handover_shipment_id = COALESCE(handover_shipment_id, $2) WHERE id = $1`, vendorOrderID, shipmentID)
	return err
}

// Counts feed the worker report: requests waiting on someone.
func (r CancellationRepository) Counts(ctx context.Context, olderThan time.Time) (open, needsReview, stale int64, err error) {
	err = connection(ctx, r.Pool).QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status = 'needs_review'),
		count(*) FILTER (WHERE status IN ('stopping_fulfillment', 'refund_pending') AND updated_at < $1)
		FROM cancellation_requests WHERE status NOT IN ('rejected', 'resolved')`, olderThan).Scan(&open, &needsReview, &stale)
	return open, needsReview, stale, err
}
