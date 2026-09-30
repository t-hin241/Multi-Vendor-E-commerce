package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
)

var ErrReturnRequestNotFound = errors.New("repository: return request not found")

type ReturnRequestRepository struct{ pool *pgxpool.Pool }

func NewReturnRequestRepository(pool *pgxpool.Pool) *ReturnRequestRepository {
	return &ReturnRequestRepository{pool: pool}
}

const returnRequestColumns = `rr.id, rr.order_id, rr.order_item_id, rr.buyer_id, rr.reason, rr.status, rr.quantity, rr.refund_amount,
	rr.policy_version, rr.return_window_days, rr.evidence, rr.vendor_note, rr.vendor_confirmed_by, rr.vendor_confirmed_at,
	rr.decided_by, rr.decision_note, rr.decided_at, rr.received_by, rr.received_at, rr.inspection_note, rr.restock, rr.version,
	rr.created_at, rr.updated_at`

func scanReturnRequest(row pgx.Row) (*domain.ReturnRequest, error) {
	var r domain.ReturnRequest
	if err := row.Scan(&r.ID, &r.OrderID, &r.OrderItemID, &r.BuyerID, &r.Reason, &r.Status, &r.Quantity, &r.RefundAmount,
		&r.PolicyVersion, &r.ReturnWindowDays, &r.Evidence, &r.VendorNote, &r.VendorConfirmedBy, &r.VendorConfirmedAt,
		&r.DecidedBy, &r.DecisionNote, &r.DecidedAt, &r.ReceivedBy, &r.ReceivedAt, &r.InspectionNote, &r.Restock, &r.Version,
		&r.CreatedAt, &r.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrReturnRequestNotFound
		}
		return nil, err
	}
	return &r, nil
}

// Create inserts a request the use case already validated against the
// policy. One request per order item.
func (r *ReturnRequestRepository) Create(ctx context.Context, rr *domain.ReturnRequest) error {
	err := connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO return_requests (order_id, order_item_id, buyer_id, reason, quantity, refund_amount, policy_version, return_window_days, evidence)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, status, version, created_at, updated_at`,
		rr.OrderID, rr.OrderItemID, rr.BuyerID, rr.Reason, rr.Quantity, rr.RefundAmount, rr.PolicyVersion, rr.ReturnWindowDays, rr.Evidence,
	).Scan(&rr.ID, &rr.Status, &rr.Version, &rr.CreatedAt, &rr.UpdatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return apperror.Conflict("This item already has an open return request")
	}
	return err
}

// ReturnedQuantity sums the item's units in requests that were not
// rejected. Callers hold the order lock so concurrent requests serialize.
func (r *ReturnRequestRepository) ReturnedQuantity(ctx context.Context, itemID string) (int64, error) {
	var n int64
	err := connection(ctx, r.pool).QueryRow(ctx,
		`SELECT COALESCE(SUM(quantity), 0) FROM return_requests WHERE order_item_id = $1 AND status <> 'rejected'`, itemID).Scan(&n)
	return n, err
}

// FindByID returns a request; inside a transaction the row is locked.
func (r *ReturnRequestRepository) FindByID(ctx context.Context, id string) (*domain.ReturnRequest, error) {
	lock := ""
	if _, ok := ctx.Value(transactionKey{}).(pgx.Tx); ok {
		lock = " FOR UPDATE"
	}
	return scanReturnRequest(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+returnRequestColumns+` FROM return_requests rr WHERE rr.id = $1`+lock, id))
}

// VendorOf returns the vendor that sold the returned item.
func (r *ReturnRequestRepository) VendorOf(ctx context.Context, id string) (vendorOrderID, vendorID string, err error) {
	err = connection(ctx, r.pool).QueryRow(ctx, `SELECT vo.id, vo.vendor_id FROM return_requests rr
		JOIN order_items oi ON oi.id = rr.order_item_id JOIN vendor_orders vo ON vo.id = oi.vendor_order_id WHERE rr.id = $1`, id).
		Scan(&vendorOrderID, &vendorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrReturnRequestNotFound
	}
	return
}

// ReturnUpdate carries the fields a transition sets; nil fields are kept.
type ReturnUpdate struct {
	VendorNote     *string
	VendorActor    *string
	DecisionNote   *string
	DecidedBy      *string
	ReceivedBy     *string
	InspectionNote *string
	Restock        *bool
}

// Transition moves a request from one status to another (compare-and-set
// on status and version) and sets the step's fields.
func (r *ReturnRequestRepository) Transition(ctx context.Context, rr *domain.ReturnRequest, to domain.ReturnRequestStatus, u ReturnUpdate) error {
	tag, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE return_requests SET status = $4, version = version + 1, updated_at = now(),
		    vendor_note = COALESCE($5, vendor_note),
		    vendor_confirmed_by = COALESCE($6, vendor_confirmed_by),
		    vendor_confirmed_at = CASE WHEN $6::uuid IS NOT NULL THEN now() ELSE vendor_confirmed_at END,
		    decision_note = COALESCE($7, decision_note),
		    decided_by = COALESCE($8, decided_by),
		    decided_at = CASE WHEN $8::uuid IS NOT NULL THEN now() ELSE decided_at END,
		    received_by = COALESCE($9, received_by),
		    received_at = CASE WHEN $9::uuid IS NOT NULL THEN now() ELSE received_at END,
		    inspection_note = COALESCE($10, inspection_note),
		    restock = COALESCE($11, restock)
		WHERE id = $1 AND status = $2 AND version = $3`,
		rr.ID, rr.Status, rr.Version, to, u.VendorNote, u.VendorActor, u.DecisionNote, u.DecidedBy, u.ReceivedBy, u.InspectionNote, u.Restock)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleState
	}
	rr.Status = to
	rr.Version++
	return nil
}

// AddEvent appends one audit entry in the caller's transaction.
func (r *ReturnRequestRepository) AddEvent(ctx context.Context, e *domain.ReturnEvent) error {
	return connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO return_request_events (return_request_id, actor_user_id, actor_role, action, from_status, to_status, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at`,
		e.ReturnID, e.ActorUserID, e.ActorRole, e.Action, e.FromStatus, e.ToStatus, e.Note).Scan(&e.ID, &e.CreatedAt)
}

func (r *ReturnRequestRepository) ListEvents(ctx context.Context, returnID string) ([]*domain.ReturnEvent, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT id, return_request_id, actor_user_id, actor_role, action, from_status, to_status, note, created_at
		FROM return_request_events WHERE return_request_id = $1 ORDER BY created_at, id`, returnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.ReturnEvent{}
	for rows.Next() {
		var e domain.ReturnEvent
		if err := rows.Scan(&e.ID, &e.ReturnID, &e.ActorUserID, &e.ActorRole, &e.Action, &e.FromStatus, &e.ToStatus, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

func (r *ReturnRequestRepository) ListByBuyer(ctx context.Context, buyerID string, limit, offset int) ([]*domain.ReturnRequest, error) {
	return r.list(ctx, `SELECT `+returnRequestColumns+` FROM return_requests rr WHERE rr.buyer_id = $1
		ORDER BY rr.created_at DESC, rr.id DESC LIMIT $2 OFFSET $3`, buyerID, limit, offset)
}

func (r *ReturnRequestRepository) ListByOrder(ctx context.Context, orderID string) ([]*domain.ReturnRequest, error) {
	return r.list(ctx, `SELECT `+returnRequestColumns+` FROM return_requests rr WHERE rr.order_id = $1 ORDER BY rr.created_at, rr.id`, orderID)
}

func (r *ReturnRequestRepository) ListByStatus(ctx context.Context, status string, limit, offset int) ([]*domain.ReturnRequest, error) {
	return r.list(ctx, `SELECT `+returnRequestColumns+` FROM return_requests rr WHERE ($1 = '' OR rr.status = $1)
		ORDER BY rr.created_at DESC, rr.id DESC LIMIT $2 OFFSET $3`, status, limit, offset)
}

// ListForVendor lists returns of items one vendor sold.
func (r *ReturnRequestRepository) ListForVendor(ctx context.Context, vendorID, status string, limit, offset int) ([]*domain.ReturnRequest, error) {
	return r.list(ctx, `SELECT `+returnRequestColumns+` FROM return_requests rr
		JOIN order_items oi ON oi.id = rr.order_item_id JOIN vendor_orders vo ON vo.id = oi.vendor_order_id
		WHERE vo.vendor_id = $1 AND ($2 = '' OR rr.status = $2)
		ORDER BY rr.created_at DESC, rr.id DESC LIMIT $3 OFFSET $4`, vendorID, status, limit, offset)
}

func (r *ReturnRequestRepository) list(ctx context.Context, q string, args ...any) ([]*domain.ReturnRequest, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.ReturnRequest{}
	for rows.Next() {
		item, err := scanReturnRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
