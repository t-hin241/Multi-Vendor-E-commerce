package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/vendorsvc/internal/domain"
)

// AvailabilityRepository stores AF-18 vacation intervals, the shop's
// accepting_orders flag and their append-only audit. Callers lock the shop
// row (VendorRepository.FindByID in a transaction) before touching its
// interval, so the owner and the scheduler take locks in the same order.
type AvailabilityRepository struct{ Pool *pgxpool.Pool }

// ErrStaleAvailability: expected_version no longer matches.
var ErrStaleAvailability = errors.New("availability version changed")

// AvailabilityAudit is one history row (ActorUserID nil = scheduler).
type AvailabilityAudit struct {
	VendorID, IntervalID, Action string
	ActorUserID                  *string
	StartsAt                     time.Time
	EndsAt                       *time.Time
	AvailabilityVersion          int64
	SaleVersion                  int64
	MembershipVersion            *int64
	RequestID                    string
}

const intervalColumns = `id, vendor_id, status, starts_at, ends_at, message, created_by, updated_by, paused_version, resumed_version,
	paused_at, closed_at, created_at, updated_at`

func scanInterval(row pgx.Row) (*domain.AvailabilityInterval, error) {
	var i domain.AvailabilityInterval
	err := row.Scan(&i.ID, &i.VendorID, &i.Status, &i.StartsAt, &i.EndsAt, &i.Message, &i.CreatedBy, &i.UpdatedBy, &i.PausedVersion,
		&i.ResumedVersion, &i.PausedAt, &i.ClosedAt, &i.CreatedAt, &i.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	i.StartsAt = i.StartsAt.UTC()
	if i.EndsAt != nil {
		e := i.EndsAt.UTC()
		i.EndsAt = &e
	}
	return &i, nil
}

// FindOpen returns the shop's scheduled or active interval, or nil.
func (r AvailabilityRepository) FindOpen(ctx context.Context, vendorID string) (*domain.AvailabilityInterval, error) {
	return scanInterval(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+intervalColumns+` FROM vendor_availability_intervals
		WHERE vendor_id = $1 AND status IN ('scheduled', 'active')`+lockVendor(ctx), vendorID))
}

// Create inserts a scheduled interval and returns it.
func (r AvailabilityRepository) Create(ctx context.Context, vendorID, actor string, start time.Time, end *time.Time, message string) (*domain.AvailabilityInterval, error) {
	return scanInterval(connection(ctx, r.Pool).QueryRow(ctx, `INSERT INTO vendor_availability_intervals
		(vendor_id, status, starts_at, ends_at, message, created_by) VALUES ($1, 'scheduled', $2, $3, $4, $5) RETURNING `+intervalColumns,
		vendorID, start, end, message, actor))
}

// Reschedule changes an open interval's window and message.
func (r AvailabilityRepository) Reschedule(ctx context.Context, id, actor string, start time.Time, end *time.Time, message string) error {
	_, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE vendor_availability_intervals SET starts_at = $2, ends_at = $3, message = $4,
		updated_by = $5, updated_at = now() WHERE id = $1 AND status IN ('scheduled', 'active')`, id, start, end, message, actor)
	return err
}

// Activate marks a scheduled interval as in force from at.
func (r AvailabilityRepository) Activate(ctx context.Context, id string, saleVersion int64, at time.Time) error {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE vendor_availability_intervals SET status = 'active', paused_version = $2,
		paused_at = $3, updated_at = now() WHERE id = $1 AND status = 'scheduled'`, id, saleVersion, at)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrStaleAvailability
	}
	return err
}

// Close ends an active interval or cancels a scheduled one.
func (r AvailabilityRepository) Close(ctx context.Context, id string, status domain.IntervalStatus, actor *string, resumedVersion *int64, at time.Time) error {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE vendor_availability_intervals SET status = $2, updated_by = COALESCE($3, updated_by),
		resumed_version = $4, closed_at = $5, updated_at = now() WHERE id = $1 AND status IN ('scheduled', 'active')`,
		id, status, actor, resumedVersion, at)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrStaleAvailability
	}
	return err
}

// BumpSchedule advances the shop's schedule version if it still is
// expected (or unconditionally when expected < 0, for the scheduler).
func (r AvailabilityRepository) BumpSchedule(ctx context.Context, vendorID string, expected int64) (int64, error) {
	var version int64
	err := connection(ctx, r.Pool).QueryRow(ctx, `UPDATE vendors SET availability_version = availability_version + 1, updated_at = now()
		WHERE id = $1 AND ($2 < 0 OR availability_version = $2) RETURNING availability_version`, vendorID, expected).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrStaleAvailability
	}
	return version, err
}

// SetAccepting flips accepting_orders and bumps the selling version, which
// the caller queues on the status outbox in the same transaction.
func (r AvailabilityRepository) SetAccepting(ctx context.Context, vendorID string, accepting bool) (int64, error) {
	var version int64
	err := connection(ctx, r.Pool).QueryRow(ctx, `UPDATE vendors SET accepting_orders = $2, version = version + 1, updated_at = now()
		WHERE id = $1 AND accepting_orders <> $2 RETURNING version`, vendorID, accepting).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrStaleAvailability
	}
	return version, err
}

// Due lists shops whose interval should start or end now.
func (r AvailabilityRepository) Due(ctx context.Context, limit int) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := r.Pool.Query(ctx, `SELECT vendor_id::text FROM (
		SELECT vendor_id, starts_at AS due FROM vendor_availability_intervals WHERE status = 'scheduled' AND starts_at <= now()
		UNION ALL
		SELECT vendor_id, ends_at FROM vendor_availability_intervals WHERE status = 'active' AND ends_at <= now()) d
		ORDER BY due LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Audit appends one history row.
func (r AvailabilityRepository) Audit(ctx context.Context, a AvailabilityAudit) error {
	var requestID *string
	if a.RequestID != "" && len(a.RequestID) <= 64 {
		requestID = &a.RequestID
	}
	_, err := connection(ctx, r.Pool).Exec(ctx, `INSERT INTO vendor_availability_audit_logs
		(vendor_id, interval_id, actor_user_id, action, starts_at, ends_at, availability_version, sale_version, membership_version, request_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		a.VendorID, a.IntervalID, a.ActorUserID, a.Action, a.StartsAt, a.EndsAt, a.AvailabilityVersion, a.SaleVersion, a.MembershipVersion, requestID)
	return err
}
