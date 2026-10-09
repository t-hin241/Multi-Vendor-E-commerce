package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/shipment/internal/domain"
)

var (
	ErrReturnShipmentNotFound = errors.New("repository: return shipment not found")
	// ErrActiveReturnShipment: the return already has a parcel that can move.
	ErrActiveReturnShipment = errors.New("repository: an active return shipment exists")
)

// ReturnShipmentRepository stores AF-05 return parcels.
type ReturnShipmentRepository struct{ Pool *pgxpool.Pool }

const returnShipmentColumns = `id, return_id, order_id, vendor_id, buyer_id, authorization_version, operation_id, status, recipient_name, phone,
	province, district, ward, street_address, receiving_hours, carrier_name, tracking_number, dispatched_at, dispatch_operation_id,
	received_at, exception_reason, exception_at, version, created_at, updated_at`

func scanReturnShipment(row pgx.Row) (*domain.ReturnShipment, error) {
	var s domain.ReturnShipment
	err := row.Scan(&s.ID, &s.ReturnID, &s.OrderID, &s.VendorID, &s.BuyerID, &s.AuthorizationVersion, &s.OperationID, &s.Status,
		&s.RecipientName, &s.Phone, &s.Province, &s.District, &s.Ward, &s.StreetAddress, &s.ReceivingHours, &s.CarrierName,
		&s.TrackingNumber, &s.DispatchedAt, &s.DispatchOperationID, &s.ReceivedAt, &s.ExceptionReason, &s.ExceptionAt, &s.Version,
		&s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrReturnShipmentNotFound
	}
	return &s, err
}

func (r ReturnShipmentRepository) lock(ctx context.Context) string {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return " FOR UPDATE"
	}
	return ""
}

func (r ReturnShipmentRepository) Create(ctx context.Context, s *domain.ReturnShipment) error {
	err := connection(ctx, r.Pool).QueryRow(ctx, `INSERT INTO return_shipments (return_id, order_id, vendor_id, buyer_id, authorization_version,
		operation_id, status, recipient_name, phone, province, district, ward, street_address, receiving_hours)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) RETURNING id, version, created_at, updated_at`,
		s.ReturnID, s.OrderID, s.VendorID, s.BuyerID, s.AuthorizationVersion, s.OperationID, s.Status, s.RecipientName, s.Phone,
		s.Province, s.District, s.Ward, s.StreetAddress, s.ReceivingHours).Scan(&s.ID, &s.Version, &s.CreatedAt, &s.UpdatedAt)
	switch {
	case isUniqueViolation(err, "return_shipments_active_idx"):
		return ErrActiveReturnShipment
	case isUniqueViolation(err, ""):
		return ErrShipmentAlreadyExists
	}
	return err
}

// FindByID reads a parcel; inside a transaction it is locked.
func (r ReturnShipmentRepository) FindByID(ctx context.Context, id string) (*domain.ReturnShipment, error) {
	return scanReturnShipment(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+returnShipmentColumns+` FROM return_shipments WHERE id = $1`+r.lock(ctx), id))
}

func (r ReturnShipmentRepository) FindByOperation(ctx context.Context, operationID string) (*domain.ReturnShipment, error) {
	return scanReturnShipment(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+returnShipmentColumns+` FROM return_shipments WHERE operation_id = $1`, operationID))
}

// FindActive is the return's parcel that can still move; inside a
// transaction it is locked.
func (r ReturnShipmentRepository) FindActive(ctx context.Context, returnID string) (*domain.ReturnShipment, error) {
	return scanReturnShipment(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+returnShipmentColumns+` FROM return_shipments
		WHERE return_id = $1 AND status IN ('pending_dispatch', 'in_transit')`+r.lock(ctx), returnID))
}

// Save writes the parcel with compare-and-set on its version.
func (r ReturnShipmentRepository) Save(ctx context.Context, s *domain.ReturnShipment) error {
	err := connection(ctx, r.Pool).QueryRow(ctx, `UPDATE return_shipments SET authorization_version = $3, status = $4, recipient_name = $5,
		phone = $6, province = $7, district = $8, ward = $9, street_address = $10, receiving_hours = $11, carrier_name = $12,
		tracking_number = $13, dispatched_at = $14, dispatch_operation_id = $15, received_at = $16, exception_reason = $17,
		exception_at = $18, version = version + 1, updated_at = now() WHERE id = $1 AND version = $2 RETURNING version, updated_at`,
		s.ID, s.Version, s.AuthorizationVersion, s.Status, s.RecipientName, s.Phone, s.Province, s.District, s.Ward, s.StreetAddress,
		s.ReceivingHours, s.CarrierName, s.TrackingNumber, s.DispatchedAt, s.DispatchOperationID, s.ReceivedAt, s.ExceptionReason,
		s.ExceptionAt).Scan(&s.Version, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleState
	}
	return err
}

// List is the admin view; "stale" lists parcels in transit too long.
func (r ReturnShipmentRepository) List(ctx context.Context, status string, limit, offset int) ([]*domain.ReturnShipment, error) {
	where := `($1 = '' OR status = $1)`
	if status == "stale" {
		where = `status = 'in_transit' AND dispatched_at < now() - $1::interval`
		status = domain.ReturnTransitStale.String()
	}
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+returnShipmentColumns+` FROM return_shipments WHERE `+where+`
		ORDER BY updated_at DESC, id LIMIT $2 OFFSET $3`, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.ReturnShipment{}
	for rows.Next() {
		s, err := scanReturnShipment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Counts feed the worker report.
func (r ReturnShipmentRepository) Counts(ctx context.Context) (pending, inTransit, stale int64, err error) {
	err = connection(ctx, r.Pool).QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'pending_dispatch'),
		count(*) FILTER (WHERE status = 'in_transit'), count(*) FILTER (WHERE status = 'in_transit' AND dispatched_at < $1)
		FROM return_shipments WHERE status IN ('pending_dispatch', 'in_transit')`, time.Now().Add(-domain.ReturnTransitStale)).
		Scan(&pending, &inTransit, &stale)
	return pending, inTransit, stale, err
}
