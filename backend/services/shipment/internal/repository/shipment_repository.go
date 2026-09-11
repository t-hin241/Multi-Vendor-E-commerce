package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/shipment/internal/domain"
)

var (
	ErrShipmentNotFound      = errors.New("repository: shipment not found")
	ErrShipmentAlreadyExists = errors.New("repository: shipment already exists for this vendor order")
)

const shipmentColumns = `id, vendor_order_id, vendor_id, buyer_id, status, carrier_id, tracking_number,
	zone_id, zone_name, fee_rule_id, fee_amount, package_weight_grams,
	recipient_name, phone, province, district, ward, street_address,
	shipped_at, delivered_at, intercept_provider_ref, intercept_requested_at, intercept_resolved_at,
	created_at, updated_at`

type ShipmentRepository struct {
	pool *pgxpool.Pool
}

func NewShipmentRepository(pool *pgxpool.Pool) *ShipmentRepository {
	return &ShipmentRepository{pool: pool}
}

func scanShipment(row pgx.Row) (*domain.Shipment, error) {
	var s domain.Shipment
	err := row.Scan(
		&s.ID, &s.VendorOrderID, &s.VendorID, &s.BuyerID, &s.Status, &s.CarrierID, &s.TrackingNumber,
		&s.ZoneID, &s.ZoneName, &s.FeeRuleID, &s.FeeAmount, &s.PackageWeightGrams,
		&s.RecipientName, &s.Phone, &s.Province, &s.District, &s.Ward, &s.StreetAddress,
		&s.ShippedAt, &s.DeliveredAt, &s.InterceptProviderRef, &s.InterceptRequestedAt, &s.InterceptResolvedAt,
		&s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrShipmentNotFound
		}
		return nil, err
	}
	return &s, nil
}

// Create persists a shipment with every snapshot field already resolved by
// the usecase (carrier, zone, fee rule, fee amount, package weight,
// destination) — this repository never computes anything, it only stores
// what it's given.
func (r *ShipmentRepository) Create(ctx context.Context, s *domain.Shipment) error {
	const query = `
		INSERT INTO shipments (
			vendor_order_id, vendor_id, buyer_id, status, carrier_id,
			zone_id, zone_name, fee_rule_id, fee_amount, package_weight_grams,
			recipient_name, phone, province, district, ward, street_address
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING id, created_at, updated_at`

	err := r.pool.QueryRow(ctx, query,
		s.VendorOrderID, s.VendorID, s.BuyerID, s.Status, s.CarrierID,
		s.ZoneID, s.ZoneName, s.FeeRuleID, s.FeeAmount, s.PackageWeightGrams,
		s.RecipientName, s.Phone, s.Province, s.District, s.Ward, s.StreetAddress,
	).Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrShipmentAlreadyExists
		}
		return err
	}
	return nil
}

func (r *ShipmentRepository) FindByID(ctx context.Context, id string) (*domain.Shipment, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE id = $1`, id)
	return scanShipment(row)
}

func (r *ShipmentRepository) FindByVendorOrderID(ctx context.Context, vendorOrderID string) (*domain.Shipment, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE vendor_order_id = $1`, vendorOrderID)
	return scanShipment(row)
}

func (r *ShipmentRepository) ListByVendor(ctx context.Context, vendorID string, limit, offset int) ([]*domain.Shipment, error) {
	return r.list(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE vendor_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, vendorID, limit, offset)
}

// ListByBuyer backs the buyer's own shipment views — buyer_id is
// denormalized onto shipments at creation time, so this never needs a
// live cross-service ownership check.
func (r *ShipmentRepository) ListByBuyer(ctx context.Context, buyerID string, limit, offset int) ([]*domain.Shipment, error) {
	return r.list(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE buyer_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, buyerID, limit, offset)
}

func (r *ShipmentRepository) list(ctx context.Context, query string, args ...any) ([]*domain.Shipment, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var shipments []*domain.Shipment
	for rows.Next() {
		s, err := scanShipment(rows)
		if err != nil {
			return nil, err
		}
		shipments = append(shipments, s)
	}
	return shipments, rows.Err()
}

// Advance updates a shipment's status and, when provided (non-nil), its
// tracking number/shipped_at/delivered_at — a nil argument leaves that
// column untouched, via COALESCE against a bound SQL NULL. Carrier is
// fixed at creation time and never touched here.
func (r *ShipmentRepository) Advance(ctx context.Context, id string, status domain.Status, trackingNumber *string, shippedAt, deliveredAt *time.Time) error {
	const query = `
		UPDATE shipments
		SET status = $1,
		    tracking_number = COALESCE($2, tracking_number),
		    shipped_at = COALESCE($3, shipped_at),
		    delivered_at = COALESCE($4, delivered_at),
		    updated_at = now()
		WHERE id = $5`

	tag, err := r.pool.Exec(ctx, query, status, trackingNumber, shippedAt, deliveredAt, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrShipmentNotFound
	}
	return nil
}

// RequestInterception atomically moves a shipment from "shipped" to
// "interception_requested" and records the carrier's reference id — the
// WHERE clause is the concurrency guard: a shipment that has since moved on
// (delivered, or a second interception request racing the first) leaves
// applied=false rather than erroring, since this call is always best-effort
// from Order's perspective.
func (r *ShipmentRepository) RequestInterception(ctx context.Context, id, providerRef string) (bool, error) {
	const query = `
		UPDATE shipments
		SET status = 'interception_requested',
		    intercept_provider_ref = $1,
		    intercept_requested_at = now(),
		    updated_at = now()
		WHERE id = $2 AND status = 'shipped'`

	tag, err := r.pool.Exec(ctx, query, providerRef, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ResolveInterception atomically applies the carrier's decision — the
// WHERE clause (status = 'interception_requested' AND intercept_resolved_at
// IS NULL) is what makes processing a duplicate webhook delivery a no-op
// (applied=false) instead of double-applying the outcome.
func (r *ShipmentRepository) ResolveInterception(ctx context.Context, id string, newStatus domain.Status) (*domain.Shipment, bool, error) {
	const query = `
		UPDATE shipments
		SET status = $1,
		    intercept_resolved_at = now(),
		    updated_at = now()
		WHERE id = $2 AND status = 'interception_requested' AND intercept_resolved_at IS NULL
		RETURNING ` + shipmentColumns

	row := r.pool.QueryRow(ctx, query, newStatus, id)
	s, err := scanShipment(row)
	if err != nil {
		if errors.Is(err, ErrShipmentNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return s, true, nil
}

// FindByInterceptProviderRef is the carrier webhook's lookup path — the
// decision only carries the carrier's own reference id, not our shipment
// id, mirroring Payment's FindByProviderIntentID.
func (r *ShipmentRepository) FindByInterceptProviderRef(ctx context.Context, providerRef string) (*domain.Shipment, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE intercept_provider_ref = $1`, providerRef)
	return scanShipment(row)
}
