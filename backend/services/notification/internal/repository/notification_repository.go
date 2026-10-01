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

var (
	ErrNotFound = errors.New("repository: notification not found")
	// ErrStale: the notification is no longer held by this worker attempt
	// (its lease expired and another worker took it).
	ErrStale = errors.New("repository: notification changed meanwhile")
)

type NotificationRepository struct {
	pool *pgxpool.Pool
}

func NewNotificationRepository(pool *pgxpool.Pool) *NotificationRepository {
	return &NotificationRepository{pool: pool}
}

const columns = `id, event_id, source, user_id, type, template_version, reference_id, correlation_id, dedup_key, status,
	recipient_masked, fail_reason, attempts, max_attempts, next_attempt_at, sent_at, created_at, updated_at`

func scan(row pgx.Row) (*domain.Notification, error) {
	var n domain.Notification
	err := row.Scan(&n.ID, &n.EventID, &n.Source, &n.UserID, &n.Type, &n.TemplateVersion, &n.ReferenceID, &n.CorrelationID, &n.DedupKey,
		&n.Status, &n.RecipientMasked, &n.FailReason, &n.Attempts, &n.MaxAttempts, &n.NextAttemptAt, &n.SentAt, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &n, err
}

func scanAll(rows pgx.Rows, err error) ([]*domain.Notification, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.Notification{}
	for rows.Next() {
		n, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Insert records a pending notification once per dedup key. It returns
// the stored row and whether this call created it.
func (r *NotificationRepository) Insert(ctx context.Context, n *domain.Notification) (*domain.Notification, bool, error) {
	stored, err := scan(connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO notifications (event_id, source, user_id, type, template_version, reference_id, correlation_id, dedup_key,
			status, max_attempts, next_attempt_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', $9, $10)
		ON CONFLICT (dedup_key) DO NOTHING
		RETURNING `+columns,
		n.EventID, n.Source, n.UserID, n.Type, n.TemplateVersion, n.ReferenceID, n.CorrelationID, n.DedupKey, n.MaxAttempts, n.NextAttemptAt))
	if err == nil {
		return stored, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}
	stored, err = scan(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+columns+` FROM notifications WHERE dedup_key = $1`, n.DedupKey))
	return stored, false, err
}

func (r *NotificationRepository) FindByID(ctx context.Context, id string) (*domain.Notification, error) {
	return scan(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+columns+` FROM notifications WHERE id = $1`, id))
}

// ParkStuck parks notifications whose worker stopped during their last
// attempt (lease expired): they are not taken again automatically.
func (r *NotificationRepository) ParkStuck(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		WITH stuck AS (
			UPDATE notifications SET status = 'parked', lease_until = NULL, updated_at = now(),
				fail_reason = 'stopped during the last attempt; the message may have been sent'
			WHERE status = 'sending' AND lease_until < now() AND attempts >= max_attempts
			RETURNING id, attempts)
		INSERT INTO notification_attempts (notification_id, attempt, outcome, error)
		SELECT id, attempts, 'parked', 'worker stopped during the attempt' FROM stuck`)
	return tag.RowsAffected(), err
}

// ClaimTask takes attempt number attempt of notification id for lease. It
// succeeds only if the notification is at the previous attempt and free
// (pending, or sending with an expired lease), so a duplicate, stale or
// early job finds nothing to do (ErrNotFound).
func (r *NotificationRepository) ClaimTask(ctx context.Context, id string, attempt int, lease time.Duration) (*domain.Notification, error) {
	return scan(r.pool.QueryRow(ctx, `
		UPDATE notifications SET status = 'sending', attempts = attempts + 1, lease_until = now() + $3 * interval '1 second', updated_at = now()
		WHERE id = $1 AND attempts = $2 - 1 AND attempts < max_attempts
		  AND next_attempt_at <= now() + interval '5 seconds'
		  AND (status = 'pending' OR (status = 'sending' AND lease_until < now()))
		RETURNING `+columns, id, attempt, int(lease.Seconds())))
}

// Due names the next attempt a notification is waiting for.
type Due struct {
	ID      string
	Attempt int
}

// Unqueued lists notifications that should have been delivered by now:
// pending for longer than grace past their time, or sending with an
// expired lease. Their job may have been lost (Redis data loss, a failed
// enqueue), so the caller queues it again; an existing job is kept.
func (r *NotificationRepository) Unqueued(ctx context.Context, grace time.Duration, limit int) ([]Due, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, attempts + 1 FROM notifications
		WHERE attempts < max_attempts AND (
			(status = 'pending' AND next_attempt_at < now() - $1 * interval '1 second')
			OR (status = 'sending' AND lease_until < now()))
		ORDER BY next_attempt_at, id LIMIT $2`, int(grace.Seconds()), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Due{}
	for rows.Next() {
		var d Due
		if err := rows.Scan(&d.ID, &d.Attempt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Outcome is the result of one attempt.
type Outcome struct {
	Status          domain.Status // sent, pending (retry), failed, parked
	Reason          *string
	RecipientMasked *string
	NextAttemptAt   time.Time
	Duration        time.Duration
}

// Finish records attempt number attempt and its outcome, only if this
// worker still holds the notification at that attempt.
func (r *NotificationRepository) Finish(ctx context.Context, id string, attempt int, o Outcome) error {
	return (Transactions{Pool: r.pool}).Run(ctx, func(ctx context.Context) error {
		q := connection(ctx, r.pool)
		tag, err := q.Exec(ctx, `
			UPDATE notifications SET status = $3, fail_reason = $4, recipient_masked = COALESCE($5, recipient_masked),
				next_attempt_at = $6, lease_until = NULL, updated_at = now(),
				sent_at = CASE WHEN $3 = 'sent' THEN now() ELSE sent_at END
			WHERE id = $1 AND status = 'sending' AND attempts = $2`,
			id, attempt, o.Status, o.Reason, o.RecipientMasked, o.NextAttemptAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrStale
		}
		outcome := map[domain.Status]string{domain.StatusSent: "sent", domain.StatusPending: "retry", domain.StatusFailed: "failed", domain.StatusParked: "parked"}[o.Status]
		_, err = q.Exec(ctx, `INSERT INTO notification_attempts (notification_id, attempt, outcome, error, duration_ms) VALUES ($1, $2, $3, $4, $5)`,
			id, attempt, outcome, o.Reason, int(o.Duration.Milliseconds()))
		return err
	})
}

// Filter narrows the admin list.
type Filter struct {
	Status string
	Type   string
	UserID string
}

func (r *NotificationRepository) List(ctx context.Context, f Filter, limit, offset int) ([]*domain.Notification, int64, error) {
	where := `($1 = '' OR status = $1) AND ($2 = '' OR type = $2) AND ($3 = '' OR user_id::text = $3)`
	var total int64
	if err := connection(ctx, r.pool).QueryRow(ctx, `SELECT count(*) FROM notifications WHERE `+where, f.Status, f.Type, f.UserID).Scan(&total); err != nil {
		return nil, 0, err
	}
	items, err := scanAll(connection(ctx, r.pool).Query(ctx, `SELECT `+columns+` FROM notifications WHERE `+where+`
		ORDER BY created_at DESC, id LIMIT $4 OFFSET $5`, f.Status, f.Type, f.UserID, limit, offset))
	return items, total, err
}

func (r *NotificationRepository) Attempts(ctx context.Context, id string) ([]*domain.Attempt, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT id, attempt, outcome, error, duration_ms, created_at
		FROM notification_attempts WHERE notification_id = $1 ORDER BY created_at, attempt`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.Attempt{}
	for rows.Next() {
		var a domain.Attempt
		if err := rows.Scan(&a.ID, &a.Attempt, &a.Outcome, &a.Error, &a.DurationMS, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// Requeue puts a failed or parked notification back in the queue with a
// few more attempts, in the caller's transaction.
func (r *NotificationRepository) Requeue(ctx context.Context, id string, extraAttempts int) (*domain.Notification, error) {
	n, err := scan(connection(ctx, r.pool).QueryRow(ctx, `
		UPDATE notifications SET status = 'pending', max_attempts = LEAST(GREATEST(max_attempts, attempts + $2), 50),
			next_attempt_at = now(), lease_until = NULL, updated_at = now()
		WHERE id = $1 AND status IN ('failed', 'parked') RETURNING `+columns, id, extraAttempts))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrStale
	}
	return n, err
}

// RecordAudit appends an admin action in the caller's transaction.
func (r *NotificationRepository) RecordAudit(ctx context.Context, actorID, action, id string, reason *string, changes map[string]any) error {
	encoded, err := json.Marshal(changes)
	if err != nil {
		return fmt.Errorf("encode audit changes: %w", err)
	}
	_, err = connection(ctx, r.pool).Exec(ctx, `INSERT INTO notification_admin_audit (actor_id, action, entity_type, entity_id, reason, changes, request_id)
		VALUES ($1, $2, 'notification', $3, $4, $5, $6)`, actorID, action, id, reason, encoded, middleware.CorrelationID(ctx))
	return err
}

// Counts is the delivery health report (dashboard, worker log, alerts).
func (r *NotificationRepository) Counts(ctx context.Context) (map[string]int64, error) {
	var pending, sending, parked, failed24, sent24, stale, oldest, p95 int64
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status = 'pending'),
		count(*) FILTER (WHERE status = 'sending'),
		count(*) FILTER (WHERE status = 'parked'),
		count(*) FILTER (WHERE status = 'failed' AND updated_at > now() - interval '24 hours'),
		count(*) FILTER (WHERE status = 'sent' AND sent_at > now() - interval '24 hours'),
		count(*) FILTER (WHERE status IN ('pending', 'sending') AND created_at < now() - interval '15 minutes'),
		COALESCE(EXTRACT(EPOCH FROM now() - min(created_at) FILTER (WHERE status IN ('pending', 'sending'))), 0)::bigint,
		COALESCE(EXTRACT(EPOCH FROM percentile_cont(0.95) WITHIN GROUP (ORDER BY sent_at - created_at)
			FILTER (WHERE status = 'sent' AND sent_at > now() - interval '24 hours')), 0)::bigint
		FROM notifications WHERE status <> 'sent' OR sent_at > now() - interval '24 hours'`).
		Scan(&pending, &sending, &parked, &failed24, &sent24, &stale, &oldest, &p95)
	return map[string]int64{
		"pending": pending, "sending": sending, "parked": parked, "failed_24h": failed24, "sent_24h": sent24,
		"pending_over_15m": stale, "oldest_pending_seconds": oldest, "delivery_p95_seconds_24h": p95,
	}, err
}

// PurgeAttempts deletes attempt rows older than before (retention); the
// notification row with its final status stays.
func (r *NotificationRepository) PurgeAttempts(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM notification_attempts WHERE created_at < $1`, before)
	return tag.RowsAffected(), err
}

// AuditSearchSQL exposes notification_admin_audit to the admin audit
// search (pkg/adminaudit).
const AuditSearchSQL = `SELECT id::text, created_at, actor_id::text, action, entity_type, entity_id, reason, request_id, changes FROM notification_admin_audit`
