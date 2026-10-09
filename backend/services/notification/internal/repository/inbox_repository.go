package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/notification/internal/domain"
)

// InboxRepository stores people's inbox items (AF-09). Every read and
// change is scoped to the recipient taken from the session: another
// person's item is "not found".
type InboxRepository struct{ Pool *pgxpool.Pool }

const inboxColumns = `id, recipient_id, source, event_id, kind, template_version, title, body, reference_type, reference_id, link,
	read_at, hidden_at, created_at`

func scanInbox(row pgx.Row) (*domain.InboxItem, error) {
	var i domain.InboxItem
	err := row.Scan(&i.ID, &i.RecipientID, &i.Source, &i.EventID, &i.Kind, &i.TemplateVersion, &i.Title, &i.Body, &i.ReferenceType,
		&i.ReferenceID, &i.Link, &i.ReadAt, &i.HiddenAt, &i.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &i, err
}

// Add writes the item once per (source, event, recipient, kind), in the
// caller's transaction; a repeat changes nothing (read state is kept).
func (r InboxRepository) Add(ctx context.Context, i *domain.InboxItem) error {
	_, err := connection(ctx, r.Pool).Exec(ctx, `
		INSERT INTO inbox_items (recipient_id, source, event_id, kind, template_version, title, body, reference_type, reference_id, link)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (source, event_id, recipient_id, kind) DO NOTHING`,
		i.RecipientID, i.Source, i.EventID, i.Kind, i.TemplateVersion, i.Title, i.Body, i.ReferenceType, i.ReferenceID, i.Link)
	return err
}

// List returns up to limit visible items of recipient, newest first, after
// cursor when given.
func (r InboxRepository) List(ctx context.Context, recipient string, unreadOnly bool, after *domain.InboxCursor, limit int) ([]*domain.InboxItem, error) {
	args := []any{recipient, unreadOnly, limit}
	q := `SELECT ` + inboxColumns + ` FROM inbox_items
		WHERE recipient_id = $1 AND hidden_at IS NULL AND (NOT $2 OR read_at IS NULL)`
	if after != nil {
		q += ` AND (created_at, id) < ($4, $5::uuid)`
		args = append(args, after.CreatedAt, after.ID)
	}
	rows, err := r.Pool.Query(ctx, q+` ORDER BY created_at DESC, id DESC LIMIT $3`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.InboxItem{}
	for rows.Next() {
		i, err := scanInbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// MarkRead sets read_at once (a repeat keeps the first time).
func (r InboxRepository) MarkRead(ctx context.Context, recipient, id string) (*domain.InboxItem, error) {
	return scanInbox(r.Pool.QueryRow(ctx, `UPDATE inbox_items SET read_at = COALESCE(read_at, now())
		WHERE id = $1 AND recipient_id = $2 RETURNING `+inboxColumns, id, recipient))
}

// Hide hides the item from the list; it stays until the retention purge.
func (r InboxRepository) Hide(ctx context.Context, recipient, id string) (*domain.InboxItem, error) {
	return scanInbox(r.Pool.QueryRow(ctx, `UPDATE inbox_items SET hidden_at = COALESCE(hidden_at, now())
		WHERE id = $1 AND recipient_id = $2 RETURNING `+inboxColumns, id, recipient))
}

// MarkThrough marks read every visible unread item of recipient up to and
// including through (by creation time, then id): items that arrived after
// the page the person saw stay unread. ErrNotFound when through is not
// theirs.
func (r InboxRepository) MarkThrough(ctx context.Context, recipient, through string) (int64, error) {
	tag, err := r.Pool.Exec(ctx, `
		WITH mark AS (SELECT created_at, id FROM inbox_items WHERE id = $2 AND recipient_id = $1)
		UPDATE inbox_items i SET read_at = now() FROM mark
		WHERE i.recipient_id = $1 AND i.read_at IS NULL AND i.hidden_at IS NULL AND (i.created_at, i.id) <= (mark.created_at, mark.id)`,
		recipient, through)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := r.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM inbox_items WHERE id = $2 AND recipient_id = $1)`, recipient, through).
			Scan(&exists); err != nil {
			return 0, err
		}
		if !exists {
			return 0, ErrNotFound
		}
	}
	return tag.RowsAffected(), nil
}

// UnreadCount counts visible unread items (partial index on recipient).
func (r InboxRepository) UnreadCount(ctx context.Context, recipient string) (int64, error) {
	var n int64
	err := r.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_items WHERE recipient_id = $1 AND read_at IS NULL AND hidden_at IS NULL`, recipient).Scan(&n)
	return n, err
}

// Purge deletes items created before cutoff, at most batch rows per call.
// Delivery records (notifications) have their own life cycle.
func (r InboxRepository) Purge(ctx context.Context, cutoff time.Time, batch int) (int64, error) {
	tag, err := r.Pool.Exec(ctx, `DELETE FROM inbox_items WHERE id IN (
		SELECT id FROM inbox_items WHERE created_at < $1 ORDER BY created_at LIMIT $2)`, cutoff, batch)
	return tag.RowsAffected(), err
}
