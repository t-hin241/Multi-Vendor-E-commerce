package repository

import (
	"context"
	"errors"
	"shopee/backend/pkg/casesla"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/shipment/internal/domain"
)

var (
	ErrShipmentNotFound      = errors.New("repository: shipment not found")
	ErrShipmentAlreadyExists = errors.New("repository: shipment already exists for this vendor order")
)

const shipmentColumns = `id, vendor_order_id, vendor_id, buyer_id, status, version, carrier_id, tracking_number,
	zone_id, zone_name, fee_rule_id, fee_amount, package_weight_grams,
	recipient_name, phone, province, district, ward, street_address,
	shipped_at, delivered_at, returned_at, cancelled_at, tracking_updated_at, failed_attempts, last_attempt_reason,
	intercept_provider_ref, intercept_requested_at, intercept_resolved_at, address_redacted_at,
	created_at, updated_at, (SELECT due_at FROM case_sla_work_items w WHERE w.resource_type='interception' AND w.resource_id=shipments.id AND w.active), COALESCE((SELECT payload->>'waiting_on' FROM case_sla_work_items w WHERE w.resource_type='interception' AND w.resource_id=shipments.id AND w.active),'')`

type ShipmentRepository struct {
	pool *pgxpool.Pool
}

func NewShipmentRepository(pool *pgxpool.Pool) *ShipmentRepository {
	return &ShipmentRepository{pool: pool}
}

func scanShipment(row pgx.Row) (*domain.Shipment, error) {
	var s domain.Shipment
	err := row.Scan(
		&s.ID, &s.VendorOrderID, &s.VendorID, &s.BuyerID, &s.Status, &s.Version, &s.CarrierID, &s.TrackingNumber,
		&s.ZoneID, &s.ZoneName, &s.FeeRuleID, &s.FeeAmount, &s.PackageWeightGrams,
		&s.RecipientName, &s.Phone, &s.Province, &s.District, &s.Ward, &s.StreetAddress,
		&s.ShippedAt, &s.DeliveredAt, &s.ReturnedAt, &s.CancelledAt, &s.TrackingUpdatedAt, &s.FailedAttempts, &s.LastAttemptReason,
		&s.InterceptProviderRef, &s.InterceptRequestedAt, &s.InterceptResolvedAt, &s.AddressRedactedAt,
		&s.CreatedAt, &s.UpdatedAt, &s.ActionDueAt, &s.WaitingOn,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrShipmentNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Create persists a shipment with every snapshot field already resolved by
// the usecase; one per vendor order (unique index).
func (r *ShipmentRepository) Create(ctx context.Context, s *domain.Shipment) error {
	err := connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO shipments (
			vendor_order_id, vendor_id, buyer_id, status, carrier_id,
			zone_id, zone_name, fee_rule_id, fee_amount, package_weight_grams,
			recipient_name, phone, province, district, ward, street_address
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING id, version, created_at, updated_at`,
		s.VendorOrderID, s.VendorID, s.BuyerID, s.Status, s.CarrierID,
		s.ZoneID, s.ZoneName, s.FeeRuleID, s.FeeAmount, s.PackageWeightGrams,
		s.RecipientName, s.Phone, s.Province, s.District, s.Ward, s.StreetAddress,
	).Scan(&s.ID, &s.Version, &s.CreatedAt, &s.UpdatedAt)
	if isUniqueViolation(err, "") {
		return ErrShipmentAlreadyExists
	}
	return err
}

func (r *ShipmentRepository) FindByID(ctx context.Context, id string) (*domain.Shipment, error) {
	return scanShipment(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE id = $1`, id))
}

// LockByID reads the shipment for update inside a transaction.
func (r *ShipmentRepository) LockByID(ctx context.Context, id string) (*domain.Shipment, error) {
	return scanShipment(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE id = $1 FOR UPDATE`, id))
}

func (r *ShipmentRepository) FindByVendorOrderID(ctx context.Context, vendorOrderID string) (*domain.Shipment, error) {
	return scanShipment(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE vendor_order_id = $1`, vendorOrderID))
}

func (r *ShipmentRepository) ListByVendor(ctx context.Context, vendorID string, limit, offset int) ([]*domain.Shipment, error) {
	return r.list(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE vendor_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, vendorID, limit, offset)
}

// ListByBuyer backs the buyer's own shipment views; buyer_id is
// denormalized at creation, so no cross-service ownership check is needed.
func (r *ShipmentRepository) ListByBuyer(ctx context.Context, buyerID string, limit, offset int) ([]*domain.Shipment, error) {
	return r.list(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE buyer_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, buyerID, limit, offset)
}

func (r *ShipmentRepository) list(ctx context.Context, query string, args ...any) ([]*domain.Shipment, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	shipments := []*domain.Shipment{}
	for rows.Next() {
		s, err := scanShipment(rows)
		if err != nil {
			return nil, err
		}
		shipments = append(shipments, s)
	}
	return shipments, rows.Err()
}

// Change is what a status change writes besides the status itself; a nil
// field leaves the column untouched.
type Change struct {
	TrackingNumber    *string
	ShippedAt         *time.Time
	DeliveredAt       *time.Time
	ReturnedAt        *time.Time
	CancelledAt       *time.Time
	InterceptRef      *string
	InterceptAt       *time.Time
	InterceptResolved *time.Time
}

// Transition moves a shipment from one status to another only if it is
// still in from at version (compare-and-set); otherwise ErrStaleState.
func (r *ShipmentRepository) Transition(ctx context.Context, s *domain.Shipment, to domain.Status, c Change) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); !ok {
		return (Transactions{Pool: r.pool}).Run(ctx, func(ctx context.Context) error { return r.Transition(ctx, s, to, c) })
	}
	trackingChanged := c.TrackingNumber != nil
	err := connection(ctx, r.pool).QueryRow(ctx, `
		UPDATE shipments SET status = $3, version = version + 1,
			tracking_number = COALESCE($4, tracking_number),
			tracking_updated_at = CASE WHEN $5 THEN now() ELSE tracking_updated_at END,
			shipped_at = COALESCE($6, shipped_at),
			delivered_at = COALESCE($7, delivered_at),
			returned_at = COALESCE($8, returned_at),
			cancelled_at = COALESCE($9, cancelled_at),
			intercept_provider_ref = COALESCE($10, intercept_provider_ref),
			intercept_requested_at = COALESCE($11, intercept_requested_at),
			intercept_resolved_at = COALESCE($12, intercept_resolved_at),
			updated_at = now()
		WHERE id = $1 AND status = $2 AND version = $13
		RETURNING version, updated_at`,
		s.ID, s.Status, to, c.TrackingNumber, trackingChanged, c.ShippedAt, c.DeliveredAt, c.ReturnedAt, c.CancelledAt,
		c.InterceptRef, c.InterceptAt, c.InterceptResolved, s.Version).Scan(&s.Version, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleState
	}
	if err != nil {
		return err
	}
	s.Status = to
	if c.InterceptAt != nil {
		s.InterceptRequestedAt = c.InterceptAt
	}
	tx, _ := ctx.Value(txKey{}).(pgx.Tx)
	i, err := casesla.Sync(ctx, tx, s.SLAStage())
	if i != nil && i.Active {
		due := i.EffectiveDueAt()
		s.ActionDueAt = &due
		s.WaitingOn = i.WaitingOn
	} else {
		s.ActionDueAt = nil
		s.WaitingOn = ""
	}
	return err
}

// UpdateTracking corrects the tracking number of a package in transit.
func (r *ShipmentRepository) UpdateTracking(ctx context.Context, s *domain.Shipment, tracking string) error {
	err := connection(ctx, r.pool).QueryRow(ctx, `
		UPDATE shipments SET tracking_number = $3, tracking_updated_at = now(), version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $2 RETURNING version`, s.ID, s.Version, tracking).Scan(&s.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleState
	}
	return err
}

// RecordFailedAttempt counts a failed delivery attempt; the package stays
// in transit.
func (r *ShipmentRepository) RecordFailedAttempt(ctx context.Context, s *domain.Shipment, reason string) error {
	err := connection(ctx, r.pool).QueryRow(ctx, `
		UPDATE shipments SET failed_attempts = failed_attempts + 1, last_attempt_reason = $3, tracking_updated_at = now(),
			version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $2 AND status = 'shipped' RETURNING version, failed_attempts`, s.ID, s.Version, reason).
		Scan(&s.Version, &s.FailedAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleState
	}
	return err
}

// FindByInterceptProviderRef is the carrier webhook's lookup path.
func (r *ShipmentRepository) FindByInterceptProviderRef(ctx context.Context, providerRef string) (*domain.Shipment, error) {
	return scanShipment(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE intercept_provider_ref = $1`, providerRef))
}

// ListAttention returns shipments needing an operator: paid but not
// shipped, in transit without updates, or interceptions without decision.
func (r *ShipmentRepository) ListAttention(ctx context.Context, kind string, limit, offset int) ([]*domain.Shipment, error) {
	where := ""
	switch kind {
	case "fulfillment_lag":
		where = `status IN ('pending', 'ready_to_ship') AND created_at < now() - $1::interval`
	case "tracking_stale":
		where = `status = 'shipped' AND COALESCE(tracking_updated_at, shipped_at, updated_at) < now() - $1::interval`
	case "interception_pending":
		where = `status = 'interception_requested' AND intercept_requested_at < now() - $1::interval`
	case "failed_attempts":
		where = `status = 'shipped' AND failed_attempts > 0 AND $1::interval IS NOT NULL`
	default:
		return nil, errors.New("unknown attention kind")
	}
	return r.list(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE `+where+` ORDER BY updated_at LIMIT $2 OFFSET $3`,
		attentionAge(kind).String(), limit, offset)
}

func attentionAge(kind string) time.Duration {
	switch kind {
	case "fulfillment_lag":
		return domain.FulfillmentLag
	case "tracking_stale":
		return domain.TrackingStale
	case "interception_pending":
		return domain.InterceptionStale
	}
	return 0
}

// AttentionCounts counts each attention kind.
func (r *ShipmentRepository) AttentionCounts(ctx context.Context) (map[string]int64, error) {
	var lag, stale, interception, attempts int64
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status IN ('pending', 'ready_to_ship') AND created_at < now() - $1::interval),
		count(*) FILTER (WHERE status = 'shipped' AND COALESCE(tracking_updated_at, shipped_at, updated_at) < now() - $2::interval),
		count(*) FILTER (WHERE status = 'interception_requested' AND intercept_requested_at < now() - $3::interval),
		count(*) FILTER (WHERE status = 'shipped' AND failed_attempts > 0)
		FROM shipments WHERE status IN ('pending', 'ready_to_ship', 'shipped', 'interception_requested')`,
		domain.FulfillmentLag.String(), domain.TrackingStale.String(), domain.InterceptionStale.String()).Scan(&lag, &stale, &interception, &attempts)
	return map[string]int64{"fulfillment_lag": lag, "tracking_stale": stale, "interception_pending": interception, "failed_attempts": attempts}, err
}

// RedactAddresses removes the buyer's name, phone, street and ward from
// final shipments older than retention. Order keeps the order's own
// address snapshot; Shipment no longer needs it.
func (r *ShipmentRepository) RedactAddresses(ctx context.Context, retention time.Duration, limit int) (int64, error) {
	tag, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE shipments SET recipient_name = NULL, phone = NULL, street_address = NULL, ward = NULL, address_redacted_at = now()
		WHERE id IN (SELECT id FROM shipments
		             WHERE address_redacted_at IS NULL AND status IN ('delivered', 'cancelled', 'returned')
		               AND updated_at < now() - $1::interval
		             ORDER BY updated_at LIMIT $2)`, retention.String(), limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
