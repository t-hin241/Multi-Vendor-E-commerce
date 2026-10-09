package repository

import (
	"context"
	"encoding/json"
	"errors"
	"shopee/backend/pkg/casesla"
	"time"

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
	rr.created_at, rr.updated_at, rr.authorization_version, rr.authorized_at, rr.authorized_by, rr.return_destination, rr.return_fee_payer,
	rr.return_fee_cap, rr.dispatch_deadline, rr.shipping_status, rr.return_shipment_id, rr.dispatch_carrier, rr.dispatch_tracking,
	rr.dispatched_at, rr.dispatch_key, rr.dispatch_hash, rr.dispatch_overdue_at, rr.restock_quantity, rr.inspection_disputed,
	(SELECT due_at FROM case_sla_work_items w WHERE w.resource_type='return' AND w.resource_id=rr.id AND w.active), COALESCE((SELECT payload->>'waiting_on' FROM case_sla_work_items w WHERE w.resource_type='return' AND w.resource_id=rr.id AND w.active),'')`

func scanReturnRequest(row pgx.Row) (*domain.ReturnRequest, error) {
	var r domain.ReturnRequest
	var destination []byte
	if err := row.Scan(&r.ID, &r.OrderID, &r.OrderItemID, &r.BuyerID, &r.Reason, &r.Status, &r.Quantity, &r.RefundAmount,
		&r.PolicyVersion, &r.ReturnWindowDays, &r.Evidence, &r.VendorNote, &r.VendorConfirmedBy, &r.VendorConfirmedAt,
		&r.DecidedBy, &r.DecisionNote, &r.DecidedAt, &r.ReceivedBy, &r.ReceivedAt, &r.InspectionNote, &r.Restock, &r.Version,
		&r.CreatedAt, &r.UpdatedAt, &r.AuthorizationVersion, &r.AuthorizedAt, &r.AuthorizedBy, &destination, &r.FeePayer, &r.FeeCap,
		&r.DispatchDeadline, &r.ShippingStatus, &r.ReturnShipmentID, &r.DispatchCarrier, &r.DispatchTracking, &r.DispatchedAt,
		&r.DispatchKey, &r.DispatchHash, &r.DispatchOverdueAt, &r.RestockQuantity, &r.InspectionDisputed,
		&r.ActionDueAt, &r.WaitingOn); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrReturnRequestNotFound
		}
		return nil, err
	}
	if len(destination) > 0 {
		r.Destination = &domain.ReturnDestination{}
		if err := json.Unmarshal(destination, r.Destination); err != nil {
			return nil, err
		}
	}
	return &r, nil
}

// Create inserts a request the use case already validated against the
// policy. One request per order item.
func (r *ReturnRequestRepository) Create(ctx context.Context, rr *domain.ReturnRequest) error {
	if !InTransaction(ctx) {
		return (Transactions{Pool: r.pool}).Run(ctx, func(ctx context.Context) error { return r.Create(ctx, rr) })
	}
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
	if err != nil {
		return err
	}
	return r.syncSLA(ctx, rr)
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
	// AF-05: a received return's sellable units, dispute flag and the
	// shipping status that goes with the step.
	RestockQuantity    *int64
	InspectionDisputed *bool
	ShippingStatus     *string
}

// Transition moves a request from one status to another (compare-and-set
// on status and version) and sets the step's fields.
func (r *ReturnRequestRepository) Transition(ctx context.Context, rr *domain.ReturnRequest, to domain.ReturnRequestStatus, u ReturnUpdate) error {
	if !InTransaction(ctx) {
		return (Transactions{Pool: r.pool}).Run(ctx, func(ctx context.Context) error { return r.Transition(ctx, rr, to, u) })
	}
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
		    restock = COALESCE($11, restock),
		    restock_quantity = COALESCE($12, restock_quantity),
		    inspection_disputed = COALESCE($13, inspection_disputed),
		    shipping_status = COALESCE($14, shipping_status)
		WHERE id = $1 AND status = $2 AND version = $3`,
		rr.ID, rr.Status, rr.Version, to, u.VendorNote, u.VendorActor, u.DecisionNote, u.DecidedBy, u.ReceivedBy, u.InspectionNote, u.Restock,
		u.RestockQuantity, u.InspectionDisputed, u.ShippingStatus)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleState
	}
	rr.Status = to
	rr.Version++
	rr.UpdatedAt = time.Now().UTC()
	if u.RestockQuantity != nil {
		rr.RestockQuantity = u.RestockQuantity
	}
	if u.InspectionDisputed != nil {
		rr.InspectionDisputed = *u.InspectionDisputed
	}
	if u.ShippingStatus != nil {
		rr.ShippingStatus = u.ShippingStatus
	}
	return r.syncSLA(ctx, rr)
}

// SaveShipping writes a return's shipping fields (AF-05) with
// compare-and-set on its version; the status does not change.
func (r *ReturnRequestRepository) SaveShipping(ctx context.Context, rr *domain.ReturnRequest) error {
	var destination []byte
	if rr.Destination != nil {
		var err error
		if destination, err = json.Marshal(rr.Destination); err != nil {
			return err
		}
	}
	err := connection(ctx, r.pool).QueryRow(ctx, `UPDATE return_requests SET authorization_version = $3, authorized_at = $4, authorized_by = $5,
		return_destination = $6, return_fee_payer = $7, return_fee_cap = $8, dispatch_deadline = $9, shipping_status = $10,
		dispatch_carrier = $11, dispatch_tracking = $12, dispatched_at = $13, dispatch_key = $14, dispatch_hash = $15,
		dispatch_overdue_at = $16, version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $2 RETURNING version, updated_at`,
		rr.ID, rr.Version, rr.AuthorizationVersion, rr.AuthorizedAt, rr.AuthorizedBy, destination, rr.FeePayer, rr.FeeCap, rr.DispatchDeadline,
		rr.ShippingStatus, rr.DispatchCarrier, rr.DispatchTracking, rr.DispatchedAt, rr.DispatchKey, rr.DispatchHash, rr.DispatchOverdueAt).
		Scan(&rr.Version, &rr.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleState
	}
	if err != nil {
		return err
	}
	return r.syncSLA(ctx, rr)
}

// SetReturnShipment records Shipment's parcel id without a version bump
// (the buyer's view does not become stale).
func (r *ReturnRequestRepository) SetReturnShipment(ctx context.Context, returnID, shipmentID string) error {
	_, err := connection(ctx, r.pool).Exec(ctx, `UPDATE return_requests SET return_shipment_id = $2 WHERE id = $1`, returnID, shipmentID)
	return err
}

// AddReceipt appends what the shop received; a concurrent receipt of the
// same version is refused (ErrStaleState).
func (r *ReturnRequestRepository) AddReceipt(ctx context.Context, g *domain.ReturnReceipt) error {
	err := connection(ctx, r.pool).QueryRow(ctx, `INSERT INTO return_goods_receipts (return_id, receipt_version, recorded_by, actor_role,
		sellable_quantity, damaged_quantity, missing_quantity, note) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id, created_at`,
		g.ReturnID, g.Version, g.RecordedBy, g.ActorRole, g.Sellable, g.Damaged, g.Missing, g.Note).Scan(&g.ID, &g.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrStaleState
	}
	return err
}

// LatestReceipt is the return's newest receipt, or nil.
func (r *ReturnRequestRepository) LatestReceipt(ctx context.Context, returnID string) (*domain.ReturnReceipt, error) {
	var g domain.ReturnReceipt
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT id, return_id, receipt_version, recorded_by, actor_role, sellable_quantity,
		damaged_quantity, missing_quantity, note, created_at FROM return_goods_receipts WHERE return_id = $1
		ORDER BY receipt_version DESC LIMIT 1`, returnID).
		Scan(&g.ID, &g.ReturnID, &g.Version, &g.RecordedBy, &g.ActorRole, &g.Sellable, &g.Damaged, &g.Missing, &g.Note, &g.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &g, err
}

// ListDispatchOverdue lists authorized returns whose parcel is late and
// not flagged yet.
func (r *ReturnRequestRepository) ListDispatchOverdue(ctx context.Context, now time.Time, limit int) ([]*domain.ReturnRequest, error) {
	return r.list(ctx, `SELECT `+returnRequestColumns+` FROM return_requests rr WHERE rr.status = 'approved'
		AND rr.shipping_status = 'awaiting_dispatch' AND rr.dispatch_overdue_at IS NULL AND rr.dispatch_deadline < $1
		ORDER BY rr.dispatch_deadline, rr.id LIMIT $2`, now, limit)
}

// ShippingCounts feed the worker report.
func (r *ReturnRequestRepository) ShippingCounts(ctx context.Context) (missing, overdue, inTransit, disputed int64, err error) {
	err = connection(ctx, r.pool).QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status = 'approved' AND shipping_status = 'destination_missing'),
		count(*) FILTER (WHERE status = 'approved' AND shipping_status = 'awaiting_dispatch' AND dispatch_overdue_at IS NOT NULL),
		count(*) FILTER (WHERE status = 'approved' AND shipping_status = 'awaiting_verification'),
		count(*) FILTER (WHERE status = 'received' AND inspection_disputed)
		FROM return_requests WHERE status IN ('approved', 'received')`).Scan(&missing, &overdue, &inTransit, &disputed)
	return missing, overdue, inTransit, disputed, err
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

func (r *ReturnRequestRepository) syncSLA(ctx context.Context, rr *domain.ReturnRequest) error {
	tx, _ := ctx.Value(transactionKey{}).(pgx.Tx)
	i, err := casesla.Sync(ctx, tx, rr.SLAStage())
	if i != nil && i.Active {
		due := i.EffectiveDueAt()
		rr.ActionDueAt = &due
		rr.WaitingOn = i.WaitingOn
	} else {
		rr.ActionDueAt = nil
		rr.WaitingOn = ""
	}
	return err
}
