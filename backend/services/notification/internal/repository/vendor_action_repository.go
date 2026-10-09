package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/middleware"
	"shopee/backend/services/notification/internal/domain"
)

// VendorActionRepository stores AF-08 shop work events and the people's
// notice preferences.
type VendorActionRepository struct{ Pool *pgxpool.Pool }

const vendorActionColumns = `id, source, event_id, vendor_id, action_kind, purpose, reference_id, vendor_order_id, correlation_id, status,
	attempts, next_attempt_at, last_error, recipient_user_ids::text[], permission_version, resolved_at, created_at, updated_at`

func scanVendorAction(row pgx.Row) (*domain.VendorAction, error) {
	var a domain.VendorAction
	err := row.Scan(&a.ID, &a.Source, &a.EventID, &a.VendorID, &a.ActionKind, &a.Purpose, &a.ReferenceID, &a.VendorOrderID, &a.CorrelationID,
		&a.Status, &a.Attempts, &a.NextAttemptAt, &a.LastError, &a.RecipientUserIDs, &a.PermissionVersion, &a.ResolvedAt, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &a, err
}

// Record stores a producer's event once per (source, event id) and returns
// the stored row and whether this call created it.
func (r VendorActionRepository) Record(ctx context.Context, a *domain.VendorAction) (*domain.VendorAction, bool, error) {
	q := connection(ctx, r.Pool)
	stored, err := scanVendorAction(q.QueryRow(ctx, `
		INSERT INTO vendor_action_events (source, event_id, vendor_id, action_kind, purpose, reference_id, vendor_order_id, correlation_id, next_attempt_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (source, event_id) DO NOTHING
		RETURNING `+vendorActionColumns,
		a.Source, a.EventID, a.VendorID, a.ActionKind, a.Purpose, a.ReferenceID, a.VendorOrderID, a.CorrelationID, a.NextAttemptAt))
	if err == nil {
		return stored, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}
	stored, err = scanVendorAction(q.QueryRow(ctx, `SELECT `+vendorActionColumns+` FROM vendor_action_events WHERE source = $1 AND event_id = $2`,
		a.Source, a.EventID))
	return stored, false, err
}

func (r VendorActionRepository) Find(ctx context.Context, id string) (*domain.VendorAction, error) {
	q := `SELECT ` + vendorActionColumns + ` FROM vendor_action_events WHERE id = $1`
	if inTransaction(ctx) {
		q += ` FOR UPDATE`
	}
	return scanVendorAction(connection(ctx, r.Pool).QueryRow(ctx, q, id))
}

// Claim takes the next due event for lease: pending and due, or resolving
// with an expired lease (its worker stopped). ErrNotFound when none is due.
func (r VendorActionRepository) Claim(ctx context.Context, lease time.Duration) (*domain.VendorAction, error) {
	return scanVendorAction(r.Pool.QueryRow(ctx, `
		UPDATE vendor_action_events SET status = 'resolving', attempts = attempts + 1,
			lease_until = now() + $1 * interval '1 second', updated_at = now()
		WHERE id = (SELECT id FROM vendor_action_events
			WHERE (status = 'pending' AND next_attempt_at <= now()) OR (status = 'resolving' AND lease_until < now())
			ORDER BY next_attempt_at, id LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING `+vendorActionColumns, int(lease.Seconds())))
}

// Resolution is how one resolution attempt ended.
type Resolution struct {
	Status            domain.VendorActionStatus
	RecipientUserIDs  []string
	PermissionVersion *string
	Error             *string
	NextAttemptAt     time.Time
}

// Complete records the attempt's result if the worker still holds the
// event (same attempt, still resolving); otherwise ErrStale and nothing
// changes, so a slow worker cannot overwrite a newer attempt.
func (r VendorActionRepository) Complete(ctx context.Context, id string, attempt int, res Resolution) error {
	recipients := res.RecipientUserIDs
	if recipients == nil {
		recipients = []string{}
	}
	tag, err := connection(ctx, r.Pool).Exec(ctx, `
		UPDATE vendor_action_events SET status = $3, recipient_user_ids = $4::uuid[], permission_version = $5, last_error = $6,
			next_attempt_at = $7, lease_until = NULL, updated_at = now(),
			resolved_at = CASE WHEN $3 = 'resolved' THEN now() ELSE resolved_at END
		WHERE id = $1 AND attempts = $2 AND status = 'resolving'`,
		id, attempt, res.Status, recipients, res.PermissionVersion, res.Error, res.NextAttemptAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStale
	}
	return nil
}

// Requeue puts an event that needs review back to pending with a fresh set
// of attempts (admin retry).
func (r VendorActionRepository) Requeue(ctx context.Context, id string) (*domain.VendorAction, error) {
	a, err := scanVendorAction(connection(ctx, r.Pool).QueryRow(ctx, `
		UPDATE vendor_action_events SET status = 'pending', attempts = 0, next_attempt_at = now(), lease_until = NULL, updated_at = now()
		WHERE id = $1 AND status IN ('no_recipient', 'parked')
		RETURNING `+vendorActionColumns, id))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrStale
	}
	return a, err
}

// VendorActionFilter narrows the admin list.
type VendorActionFilter struct {
	Status   string
	VendorID string
}

// List is the admin view, newest first; total is the filtered count.
func (r VendorActionRepository) List(ctx context.Context, f VendorActionFilter, limit, offset int) ([]*domain.VendorAction, int64, error) {
	where, args := `WHERE ($1 = '' OR status = $1) AND ($2 = '' OR vendor_id::text = $2)`, []any{f.Status, f.VendorID}
	var total int64
	if err := r.Pool.QueryRow(ctx, `SELECT count(*) FROM vendor_action_events `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.Pool.Query(ctx, `SELECT `+vendorActionColumns+` FROM vendor_action_events `+where+`
		ORDER BY created_at DESC, id LIMIT $3 OFFSET $4`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*domain.VendorAction{}
	for rows.Next() {
		a, err := scanVendorAction(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// Counts is the resolution health report: events waiting, needing an
// admin, and waiting for more than 15 minutes.
func (r VendorActionRepository) Counts(ctx context.Context) (map[string]int64, error) {
	var pending, noRecipient, parked, stale, resolved24 int64
	err := r.Pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status IN ('pending', 'resolving')),
		count(*) FILTER (WHERE status = 'no_recipient'),
		count(*) FILTER (WHERE status = 'parked'),
		count(*) FILTER (WHERE status IN ('pending', 'resolving') AND created_at < now() - interval '15 minutes'),
		count(*) FILTER (WHERE status = 'resolved' AND resolved_at > now() - interval '24 hours')
		FROM vendor_action_events WHERE status <> 'resolved' OR resolved_at > now() - interval '24 hours'`).
		Scan(&pending, &noRecipient, &parked, &stale, &resolved24)
	return map[string]int64{"vendor_actions_pending": pending, "vendor_actions_no_recipient": noRecipient, "vendor_actions_parked": parked,
		"vendor_actions_pending_over_15m": stale, "vendor_actions_resolved_24h": resolved24}, err
}

// RecordAudit writes an admin's action on a shop notice event.
func (r VendorActionRepository) RecordAudit(ctx context.Context, actorID, action, id string, reason *string, changes map[string]any) error {
	encoded, err := json.Marshal(changes)
	if err != nil {
		return fmt.Errorf("encode audit changes: %w", err)
	}
	_, err = connection(ctx, r.Pool).Exec(ctx, `INSERT INTO notification_admin_audit (actor_id, action, entity_type, entity_id, reason, changes, request_id)
		VALUES ($1, $2, 'vendor_action_event', $3, $4, $5, $6)`, actorID, action, id, reason, encoded, middleware.CorrelationID(ctx))
	return err
}

// OptIns returns the categories each of userIDs opted into (absent: none).
func (r VendorActionRepository) OptIns(ctx context.Context, userIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT user_id::text, vendor_categories FROM notification_preferences WHERE user_id = ANY($1::uuid[])`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var cats []string
		if err := rows.Scan(&id, &cats); err != nil {
			return nil, err
		}
		out[id] = cats
	}
	return out, rows.Err()
}

// Preference is a person's notice choices; Version 0 means never saved.
type Preference struct {
	Categories     []string
	MarketingOptIn bool
	Version        int64
	UpdatedAt      *time.Time
}

// Preference reads userID's choices (in the caller's transaction, locked).
func (r VendorActionRepository) Preference(ctx context.Context, userID string) (*Preference, error) {
	q := `SELECT vendor_categories, marketing_opt_in, version, updated_at FROM notification_preferences WHERE user_id = $1`
	if inTransaction(ctx) {
		q += ` FOR UPDATE`
	}
	p := &Preference{Categories: []string{}}
	err := connection(ctx, r.Pool).QueryRow(ctx, q, userID).Scan(&p.Categories, &p.MarketingOptIn, &p.Version, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return &Preference{Categories: []string{}}, nil
	}
	return p, err
}

// SavePreference stores the choices if the stored version is still
// expectedVersion (0: not saved yet); otherwise ErrStale. A change of the
// marketing consent records its time.
func (r VendorActionRepository) SavePreference(ctx context.Context, userID string, p Preference, expectedVersion int64) (*Preference, error) {
	out := &Preference{}
	var err error
	q := connection(ctx, r.Pool)
	if expectedVersion == 0 {
		err = q.QueryRow(ctx, `INSERT INTO notification_preferences (user_id, vendor_categories, marketing_opt_in, marketing_consented_at)
			VALUES ($1, $2, $3, CASE WHEN $3 THEN now() END)
			ON CONFLICT (user_id) DO NOTHING RETURNING vendor_categories, marketing_opt_in, version, updated_at`, userID, p.Categories, p.MarketingOptIn).
			Scan(&out.Categories, &out.MarketingOptIn, &out.Version, &out.UpdatedAt)
	} else {
		err = q.QueryRow(ctx, `UPDATE notification_preferences SET vendor_categories = $2, version = version + 1, updated_at = now(),
				marketing_consented_at = CASE WHEN $3 AND NOT marketing_opt_in THEN now() ELSE marketing_consented_at END,
				marketing_withdrawn_at = CASE WHEN NOT $3 AND marketing_opt_in THEN now() ELSE marketing_withdrawn_at END,
				marketing_opt_in = $3
			WHERE user_id = $1 AND version = $4 RETURNING vendor_categories, marketing_opt_in, version, updated_at`,
			userID, p.Categories, p.MarketingOptIn, expectedVersion).
			Scan(&out.Categories, &out.MarketingOptIn, &out.Version, &out.UpdatedAt)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrStale
	}
	return out, err
}

// RecordConsent appends a marketing consent change (append-only audit).
func (r VendorActionRepository) RecordConsent(ctx context.Context, userID string, granted bool, version int64) error {
	_, err := connection(ctx, r.Pool).Exec(ctx, `INSERT INTO notification_consent_audit (user_id, consent, granted, preference_version, request_id)
		VALUES ($1, 'marketing', $2, $3, $4)`, userID, granted, version, middleware.CorrelationID(ctx))
	return err
}
