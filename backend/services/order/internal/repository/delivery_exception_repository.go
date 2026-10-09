package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/casesla"
	"shopee/backend/services/order/internal/domain"
)

var (
	ErrDeliveryExceptionNotFound = errors.New("repository: delivery exception not found")
	// ErrDeliveryExceptionExists: a case is already open for the vendor
	// order, or this shipment already has one.
	ErrDeliveryExceptionExists = errors.New("repository: delivery exception exists")
)

// DeliveryExceptionRepository stores AF-04 cases, their timeline and the
// shop's goods receipts. Writes run under the order lock the use case
// holds (withOrder).
type DeliveryExceptionRepository struct{ Pool *pgxpool.Pool }

const deliveryExceptionColumns = `id, order_id, vendor_order_id, vendor_id, buyer_id, shipment_id, exception_type, current_shipment_id, attempt_no,
	carrier_outcome, failed_attempts, detection_reason, status, resolution, policy_snapshot, hold_id, hold_status, hold_note, redelivery_count,
	replacement_shipment_id, redelivery_address, consented_at, refund_id, inventory_recovery_ref, decided_by, decided_at, decision_reason,
	review_reason, late_delivery_at, resolved_at, version, created_at, updated_at,
	(SELECT due_at FROM case_sla_work_items w WHERE w.resource_type = 'delivery_exception' AND w.resource_id = delivery_exceptions.id AND w.active),
	COALESCE((SELECT payload->>'waiting_on' FROM case_sla_work_items w WHERE w.resource_type = 'delivery_exception'
		AND w.resource_id = delivery_exceptions.id AND w.active), '')`

func scanDeliveryException(row pgx.Row) (*domain.DeliveryException, error) {
	var d domain.DeliveryException
	var policy, address []byte
	err := row.Scan(&d.ID, &d.OrderID, &d.VendorOrderID, &d.VendorID, &d.BuyerID, &d.ShipmentID, &d.ExceptionType, &d.CurrentShipmentID,
		&d.AttemptNo, &d.CarrierOutcome, &d.FailedAttempts, &d.DetectionReason, &d.Status, &d.Resolution, &policy, &d.HoldID, &d.HoldStatus,
		&d.HoldNote, &d.RedeliveryCount, &d.ReplacementShipmentID, &address, &d.ConsentedAt, &d.RefundID, &d.RecoveryRef, &d.DecidedBy,
		&d.DecidedAt, &d.DecisionReason, &d.ReviewReason, &d.LateDeliveryAt, &d.ResolvedAt, &d.Version, &d.CreatedAt, &d.UpdatedAt,
		&d.ActionDueAt, &d.WaitingOn)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDeliveryExceptionNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(policy, &d.Policy); err != nil {
		return nil, err
	}
	if len(address) > 0 {
		d.RedeliveryAddress = &domain.Destination{}
		if err := json.Unmarshal(address, d.RedeliveryAddress); err != nil {
			return nil, err
		}
	}
	return &d, nil
}

func collectDeliveryExceptions(rows pgx.Rows, err error) ([]*domain.DeliveryException, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.DeliveryException{}
	for rows.Next() {
		d, err := scanDeliveryException(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r DeliveryExceptionRepository) syncSLA(ctx context.Context, d *domain.DeliveryException) error {
	if d.Receipt == nil && d.Status == domain.DXAwaitingGoods {
		receipt, err := r.LatestReceipt(ctx, d.ID, d.CurrentShipmentID)
		if err != nil {
			return err
		}
		d.Receipt = receipt
	}
	tx, _ := ctx.Value(transactionKey{}).(pgx.Tx)
	i, err := casesla.Sync(ctx, tx, d.SLAStage())
	if i != nil && i.Active {
		due := i.EffectiveDueAt()
		d.ActionDueAt, d.WaitingOn = &due, i.WaitingOn
	} else {
		d.ActionDueAt, d.WaitingOn = nil, ""
	}
	return err
}

// Create inserts a case; a second open one for the vendor order or a
// second one for the shipment is refused by its unique index.
func (r DeliveryExceptionRepository) Create(ctx context.Context, d *domain.DeliveryException) error {
	if !InTransaction(ctx) {
		return (Transactions{Pool: r.Pool}).Run(ctx, func(ctx context.Context) error { return r.Create(ctx, d) })
	}
	err := connection(ctx, r.Pool).QueryRow(ctx, `INSERT INTO delivery_exceptions (order_id, vendor_order_id, vendor_id, buyer_id, shipment_id,
		exception_type, current_shipment_id, attempt_no, carrier_outcome, failed_attempts, detection_reason, status, policy_snapshot, hold_id,
		hold_status) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15) RETURNING id, version, created_at, updated_at`,
		d.OrderID, d.VendorOrderID, d.VendorID, d.BuyerID, d.ShipmentID, d.ExceptionType, d.CurrentShipmentID, d.AttemptNo, d.CarrierOutcome,
		d.FailedAttempts, d.DetectionReason, d.Status, d.PolicyJSON(), d.HoldID, d.HoldStatus).Scan(&d.ID, &d.Version, &d.CreatedAt, &d.UpdatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrDeliveryExceptionExists
	}
	if err != nil {
		return err
	}
	return r.syncSLA(ctx, d)
}

// FindByID reads a case; inside a transaction it is locked.
func (r DeliveryExceptionRepository) FindByID(ctx context.Context, id string) (*domain.DeliveryException, error) {
	lock := ""
	if InTransaction(ctx) {
		lock = " FOR UPDATE"
	}
	return scanDeliveryException(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+deliveryExceptionColumns+` FROM delivery_exceptions
		WHERE id = $1`+lock, id))
}

func (r DeliveryExceptionRepository) optional(d *domain.DeliveryException, err error) (*domain.DeliveryException, error) {
	if errors.Is(err, ErrDeliveryExceptionNotFound) {
		return nil, nil
	}
	return d, err
}

// FindOpen is the vendor order's open case, or nil.
func (r DeliveryExceptionRepository) FindOpen(ctx context.Context, vendorOrderID string) (*domain.DeliveryException, error) {
	return r.optional(scanDeliveryException(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+deliveryExceptionColumns+` FROM delivery_exceptions
		WHERE vendor_order_id = $1 AND status <> 'resolved'`, vendorOrderID)))
}

// FindByShipment is the case a shipment opened, or nil.
func (r DeliveryExceptionRepository) FindByShipment(ctx context.Context, shipmentID string) (*domain.DeliveryException, error) {
	return r.optional(scanDeliveryException(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+deliveryExceptionColumns+` FROM delivery_exceptions
		WHERE shipment_id = $1`, shipmentID)))
}

// FindByRefund is the case a refund pays, or nil.
func (r DeliveryExceptionRepository) FindByRefund(ctx context.Context, refundID string) (*domain.DeliveryException, error) {
	return r.optional(scanDeliveryException(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+deliveryExceptionColumns+` FROM delivery_exceptions
		WHERE refund_id = $1`, refundID)))
}

func (r DeliveryExceptionRepository) ListByOrder(ctx context.Context, orderID string) ([]*domain.DeliveryException, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+deliveryExceptionColumns+` FROM delivery_exceptions WHERE order_id = $1
		ORDER BY created_at DESC, id`, orderID)
	return collectDeliveryExceptions(rows, err)
}

func (r DeliveryExceptionRepository) ListForVendor(ctx context.Context, vendorID, status string, limit, offset int) ([]*domain.DeliveryException, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+deliveryExceptionColumns+` FROM delivery_exceptions
		WHERE vendor_id = $1 AND ($2 = '' OR status = $2 OR ($2 = 'open' AND status <> 'resolved'))
		ORDER BY created_at DESC, id LIMIT $3 OFFSET $4`, vendorID, status, limit, offset)
	return collectDeliveryExceptions(rows, err)
}

// List is the admin queue: "open" lists every case not resolved.
func (r DeliveryExceptionRepository) List(ctx context.Context, status string, limit, offset int) ([]*domain.DeliveryException, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+deliveryExceptionColumns+` FROM delivery_exceptions
		WHERE ($1 = '' OR status = $1 OR ($1 = 'open' AND status <> 'resolved'))
		ORDER BY created_at, id LIMIT $2 OFFSET $3`, status, limit, offset)
	return collectDeliveryExceptions(rows, err)
}

// Save writes a case's state with compare-and-set on its version.
func (r DeliveryExceptionRepository) Save(ctx context.Context, d *domain.DeliveryException) error {
	var address []byte
	if d.RedeliveryAddress != nil {
		var err error
		if address, err = json.Marshal(d.RedeliveryAddress); err != nil {
			return err
		}
	}
	err := connection(ctx, r.Pool).QueryRow(ctx, `UPDATE delivery_exceptions SET current_shipment_id = $3, attempt_no = $4, carrier_outcome = $5,
		failed_attempts = $6, status = $7, resolution = $8, redelivery_count = $9, replacement_shipment_id = $10, redelivery_address = $11,
		consented_at = $12, refund_id = $13, inventory_recovery_ref = $14, decided_by = $15, decided_at = $16, decision_reason = $17,
		review_reason = $18, late_delivery_at = $19, resolved_at = $20, version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $2 RETURNING version, updated_at`,
		d.ID, d.Version, d.CurrentShipmentID, d.AttemptNo, d.CarrierOutcome, d.FailedAttempts, d.Status, d.Resolution, d.RedeliveryCount,
		d.ReplacementShipmentID, address, d.ConsentedAt, d.RefundID, d.RecoveryRef, d.DecidedBy, d.DecidedAt, d.DecisionReason,
		d.ReviewReason, d.LateDeliveryAt, d.ResolvedAt).Scan(&d.Version, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleState
	}
	if err != nil {
		return err
	}
	return r.syncSLA(ctx, d)
}

// SetHold moves the case's hold from one of from to to, without a version
// bump (Payment's answer never makes an admin's view stale).
func (r DeliveryExceptionRepository) SetHold(ctx context.Context, id string, from []string, to string, note *string) (bool, error) {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE delivery_exceptions SET hold_status = $3, hold_note = $4
		WHERE id = $1 AND hold_status = ANY($2)`, id, from, to, note)
	return err == nil && tag.RowsAffected() == 1, err
}

func (r DeliveryExceptionRepository) AddEvent(ctx context.Context, e *domain.DeliveryExceptionEvent) error {
	return connection(ctx, r.Pool).QueryRow(ctx, `INSERT INTO delivery_exception_history (exception_id, actor_id, actor_role, action,
		from_status, to_status, note) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at`,
		e.ExceptionID, e.ActorID, e.ActorRole, e.Action, e.FromStatus, e.ToStatus, e.Note).Scan(&e.ID, &e.CreatedAt)
}

func (r DeliveryExceptionRepository) ListEvents(ctx context.Context, exceptionID string) ([]*domain.DeliveryExceptionEvent, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT id, exception_id, actor_id, actor_role, action, from_status, to_status, note, created_at
		FROM delivery_exception_history WHERE exception_id = $1 ORDER BY created_at, id`, exceptionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.DeliveryExceptionEvent{}
	for rows.Next() {
		var e domain.DeliveryExceptionEvent
		if err := rows.Scan(&e.ID, &e.ExceptionID, &e.ActorID, &e.ActorRole, &e.Action, &e.FromStatus, &e.ToStatus, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// AddReceipt appends a receipt version with its lines; a concurrent
// receipt of the same version is refused (ErrStaleState).
func (r DeliveryExceptionRepository) AddReceipt(ctx context.Context, g *domain.GoodsReceipt) error {
	err := connection(ctx, r.Pool).QueryRow(ctx, `INSERT INTO delivery_exception_receipts (exception_id, shipment_id, receipt_version, recorded_by,
		actor_role, note) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at`,
		g.ExceptionID, g.ShipmentID, g.Version, g.RecordedBy, g.ActorRole, g.Note).Scan(&g.ID, &g.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrStaleState
	}
	if err != nil {
		return err
	}
	for _, l := range g.Lines {
		if _, err := connection(ctx, r.Pool).Exec(ctx, `INSERT INTO delivery_exception_receipt_lines (receipt_id, order_item_id, condition, quantity)
			VALUES ($1, $2, $3, $4)`, g.ID, l.OrderItemID, l.Condition, l.Quantity); err != nil {
			return err
		}
	}
	return nil
}

// LatestReceipt is the newest receipt version for one attempt, or nil.
func (r DeliveryExceptionRepository) LatestReceipt(ctx context.Context, exceptionID, shipmentID string) (*domain.GoodsReceipt, error) {
	var g domain.GoodsReceipt
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT id, exception_id, shipment_id, receipt_version, recorded_by, actor_role, note, created_at
		FROM delivery_exception_receipts WHERE exception_id = $1 AND shipment_id = $2 ORDER BY receipt_version DESC LIMIT 1`, exceptionID, shipmentID).
		Scan(&g.ID, &g.ExceptionID, &g.ShipmentID, &g.Version, &g.RecordedBy, &g.ActorRole, &g.Note, &g.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT order_item_id, condition, quantity FROM delivery_exception_receipt_lines
		WHERE receipt_id = $1 ORDER BY order_item_id, condition`, g.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l domain.ReceiptLine
		if err := rows.Scan(&l.OrderItemID, &l.Condition, &l.Quantity); err != nil {
			return nil, err
		}
		g.Lines = append(g.Lines, l)
	}
	return &g, rows.Err()
}

// Counts feed the worker report: cases waiting on someone.
func (r DeliveryExceptionRepository) Counts(ctx context.Context, olderThan time.Time) (open, needsReview, stale, unowned int64, err error) {
	err = connection(ctx, r.Pool).QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status = 'needs_review'),
		count(*) FILTER (WHERE status IN ('redelivery_pending', 'refund_pending') AND updated_at < $1),
		count(*) FILTER (WHERE status = 'investigating' AND decided_by IS NULL AND created_at < $1)
		FROM delivery_exceptions WHERE status <> 'resolved'`, olderThan).Scan(&open, &needsReview, &stale, &unowned)
	return open, needsReview, stale, unowned, err
}
