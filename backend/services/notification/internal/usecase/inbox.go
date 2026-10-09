package usecase

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/notification/internal/domain"
	"shopee/backend/services/notification/internal/repository"
)

// InboxStore keeps people's inbox items.
type InboxStore interface {
	List(ctx context.Context, recipient string, unreadOnly bool, after *domain.InboxCursor, limit int) ([]*domain.InboxItem, error)
	MarkRead(ctx context.Context, recipient, id string) (*domain.InboxItem, error)
	Hide(ctx context.Context, recipient, id string) (*domain.InboxItem, error)
	MarkThrough(ctx context.Context, recipient, through string) (int64, error)
	UnreadCount(ctx context.Context, recipient string) (int64, error)
	Purge(ctx context.Context, cutoff time.Time, batch int) (int64, error)
}

// InboxUseCase serves a person's own inbox (AF-09). The recipient is
// always the session's user; an item of someone else is "not found".
type InboxUseCase struct {
	Store InboxStore
	// Enabled is FEATURE_NOTIFICATION_INBOX_ENABLED. Off: the API answers
	// feature_disabled (the bell hides) and no new item is written; email
	// delivery is not affected.
	Enabled bool
	// Retention is INBOX_RETENTION_DAYS (default 90 days).
	Retention time.Duration
	Log       zerolog.Logger
	Now       func() time.Time
}

var (
	errInboxDisabled = &apperror.Error{Code: "feature_disabled", Message: "The notification inbox is not enabled", Status: http.StatusNotFound}
	errNoticeMissing = apperror.NotFound("Notice not found")
)

func (uc *InboxUseCase) now() time.Time {
	if uc.Now != nil {
		return uc.Now().UTC()
	}
	return time.Now().UTC()
}

// InboxPage is one page of the inbox, newest first. NextCursor is empty on
// the last page; AsOf is when the page was read.
type InboxPage struct {
	Items      []*domain.InboxItem
	NextCursor string
	AsOf       time.Time
}

func (uc *InboxUseCase) check(userID string) error {
	if !uc.Enabled {
		return errInboxDisabled
	}
	if _, err := uuid.Parse(userID); err != nil {
		return apperror.Unauthorized("Sign in again")
	}
	return nil
}

// List returns a page of userID's inbox (limit 1-100, default 20).
func (uc *InboxUseCase) List(ctx context.Context, userID string, unreadOnly bool, cursor string, limit int) (*InboxPage, error) {
	if err := uc.check(userID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	var after *domain.InboxCursor
	if cursor != "" {
		c, err := domain.DecodeInboxCursor(cursor)
		if err != nil {
			return nil, apperror.Validation("Invalid cursor")
		}
		after = c
	}
	page := &InboxPage{AsOf: uc.now()}
	items, err := uc.Store.List(ctx, userID, unreadOnly, after, limit+1)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		page.NextCursor = domain.InboxCursor{CreatedAt: last.CreatedAt, ID: last.ID}.Encode()
	}
	page.Items = items
	return page, nil
}

func (uc *InboxUseCase) item(ctx context.Context, userID, id string, change func(context.Context, string, string) (*domain.InboxItem, error)) (*domain.InboxItem, error) {
	if err := uc.check(userID); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, errNoticeMissing
	}
	item, err := change(ctx, userID, id)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, errNoticeMissing
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return item, nil
}

// MarkRead marks one item read; repeating it keeps the first time.
func (uc *InboxUseCase) MarkRead(ctx context.Context, userID, id string) (*domain.InboxItem, error) {
	return uc.item(ctx, userID, id, func(ctx context.Context, u, i string) (*domain.InboxItem, error) { return uc.Store.MarkRead(ctx, u, i) })
}

// Hide hides one item; it is deleted later by the retention purge.
func (uc *InboxUseCase) Hide(ctx context.Context, userID, id string) (*domain.InboxItem, error) {
	return uc.item(ctx, userID, id, func(ctx context.Context, u, i string) (*domain.InboxItem, error) { return uc.Store.Hide(ctx, u, i) })
}

// MarkThrough marks read every item up to throughID (the newest the person
// has seen), so items that arrived later stay unread. It returns how many
// changed and the unread count after.
func (uc *InboxUseCase) MarkThrough(ctx context.Context, userID, throughID string) (int64, int64, error) {
	if err := uc.check(userID); err != nil {
		return 0, 0, err
	}
	if _, err := uuid.Parse(throughID); err != nil {
		return 0, 0, errNoticeMissing
	}
	affected, err := uc.Store.MarkThrough(ctx, userID, throughID)
	if errors.Is(err, repository.ErrNotFound) {
		return 0, 0, errNoticeMissing
	}
	if err != nil {
		return 0, 0, apperror.Internal(err)
	}
	unread, err := uc.Store.UnreadCount(ctx, userID)
	if err != nil {
		return 0, 0, apperror.Internal(err)
	}
	return affected, unread, nil
}

// UnreadCount is the bell's number.
func (uc *InboxUseCase) UnreadCount(ctx context.Context, userID string) (int64, error) {
	if err := uc.check(userID); err != nil {
		return 0, err
	}
	n, err := uc.Store.UnreadCount(ctx, userID)
	if err != nil {
		return 0, apperror.Internal(err)
	}
	return n, nil
}

// PurgeExpired deletes items older than the retention in batches of 1000
// (at most 20 batches a run, so one run stays short). It also runs with the
// inbox off: items already written still expire.
func (uc *InboxUseCase) PurgeExpired(ctx context.Context) (int64, error) {
	if uc.Retention <= 0 {
		return 0, nil
	}
	cutoff := uc.now().Add(-uc.Retention)
	var total int64
	for range 20 {
		n, err := uc.Store.Purge(ctx, cutoff, 1000)
		total += n
		if err != nil || n < 1000 {
			return total, err
		}
	}
	return total, nil
}
