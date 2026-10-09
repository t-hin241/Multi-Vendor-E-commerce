package repository_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/notification/internal/adapter"
	"shopee/backend/services/notification/internal/domain"
	"shopee/backend/services/notification/internal/repository"
	"shopee/backend/services/notification/internal/usecase"
)

type inboxEnv struct {
	*env
	repo  repository.InboxRepository
	inbox *usecase.InboxUseCase
	other string
}

func newInboxEnv(t *testing.T) *inboxEnv {
	e := &inboxEnv{env: newEnv(t), other: uuid.NewString()}
	e.repo = repository.InboxRepository{Pool: e.pool}
	e.inbox = &usecase.InboxUseCase{Store: e.repo, Enabled: true, Retention: 90 * 24 * time.Hour, Log: zerolog.Nop()}
	e.ids.users[e.other] = &adapter.UserSnapshot{ID: e.other, Email: "other@example.invalid", Active: true}
	return e
}

func (e *inboxEnv) notices() *usecase.NotificationUseCase {
	uc := e.useCase(roles{})
	uc.Inbox = e.repo
	return uc
}

func (e *inboxEnv) notify(t *testing.T, user, eventID string) {
	t.Helper()
	if _, _, err := e.notices().Accept(t.Context(), domain.Request{EventID: eventID, Source: "order", UserID: user, Type: domain.TypeOrderPaid,
		ReferenceID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
}

func (e *inboxEnv) page(t *testing.T, user string, unread bool, cursor string, limit int) *usecase.InboxPage {
	t.Helper()
	p, err := e.inbox.List(t.Context(), user, unread, cursor, limit)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *inboxEnv) unread(t *testing.T, user string) int64 {
	t.Helper()
	n, err := e.inbox.UnreadCount(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func notFound(err error) bool {
	var app *apperror.Error
	return errors.As(err, &app) && app.Code == apperror.CodeNotFound
}

// A notice reaches its recipient's inbox once, in the same transaction as
// the email record; a redelivered event keeps the item read; nobody else
// can read, mark or hide it.
func TestInboxIsPrivateAndSurvivesReplays(t *testing.T) {
	e := newInboxEnv(t)
	ctx := t.Context()
	req := domain.Request{EventID: "effect-paid-1", Source: "order", UserID: e.buyer, Type: domain.TypeOrderPaid, ReferenceID: "order-1"}
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := e.notices().Accept(ctx, req); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := e.count(t, `SELECT count(*) FROM inbox_items`); n != 1 || e.count(t, `SELECT count(*) FROM notifications`) != 1 {
		t.Fatalf("expected one notice and one inbox item, got %d", n)
	}
	items := e.page(t, e.buyer, false, "", 20).Items
	if len(items) != 1 || items[0].Link != "/orders/order-1" || items[0].ReadAt != nil || e.unread(t, e.buyer) != 1 {
		t.Fatalf("inbox %+v", items)
	}
	id := items[0].ID

	for _, call := range []func() error{
		func() error { _, err := e.inbox.MarkRead(ctx, e.other, id); return err },
		func() error { _, err := e.inbox.Hide(ctx, e.other, id); return err },
		func() error { _, _, err := e.inbox.MarkThrough(ctx, e.other, id); return err },
		func() error { _, err := e.inbox.MarkRead(ctx, e.buyer, "not-a-uuid"); return err },
	} {
		if err := call(); !notFound(err) {
			t.Fatalf("another person's item must be not found, got %v", err)
		}
	}
	if len(e.page(t, e.other, false, "", 20).Items) != 0 || e.unread(t, e.other) != 0 {
		t.Fatal("another person sees the item")
	}

	first, err := e.inbox.MarkRead(ctx, e.buyer, id)
	if err != nil || first.ReadAt == nil {
		t.Fatal(err)
	}
	again, _ := e.inbox.MarkRead(ctx, e.buyer, id)
	if !again.ReadAt.Equal(*first.ReadAt) {
		t.Fatal("marking read twice must keep the first time")
	}
	if _, _, err := e.notices().Accept(ctx, req); err != nil {
		t.Fatal(err)
	}
	if e.unread(t, e.buyer) != 0 {
		t.Fatal("a replayed event made the item unread again")
	}

	if _, err := e.inbox.Hide(ctx, e.buyer, id); err != nil {
		t.Fatal(err)
	}
	if len(e.page(t, e.buyer, false, "", 20).Items) != 0 || e.count(t, `SELECT count(*) FROM inbox_items WHERE hidden_at IS NOT NULL`) != 1 {
		t.Fatal("a hidden item must leave the list but stay stored")
	}
}

// Pages do not overlap; "read all" stops at the newest item the person
// saw, so a notice arriving meanwhile stays unread; the count stays right
// with concurrent marks and new notices.
func TestInboxPagesAndReadMarkers(t *testing.T) {
	e := newInboxEnv(t)
	ctx := t.Context()
	for i := range 25 {
		e.notify(t, e.buyer, fmt.Sprintf("effect-%02d", i))
	}
	seen := map[string]bool{}
	cursor, pages := "", 0
	for {
		p := e.page(t, e.buyer, false, cursor, 10)
		pages++
		for _, i := range p.Items {
			if seen[i.ID] {
				t.Fatal("pages overlap")
			}
			seen[i.ID] = true
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if len(seen) != 25 || pages != 3 {
		t.Fatalf("expected 25 items on 3 pages, got %d on %d", len(seen), pages)
	}
	if _, err := e.inbox.List(ctx, e.buyer, false, "garbage", 10); err == nil {
		t.Fatal("a bad cursor must be refused")
	}

	top := e.page(t, e.buyer, false, "", 10).Items
	newest := top[0].ID
	e.notify(t, e.buyer, "effect-late") // arrives after the page was loaded
	var wg sync.WaitGroup
	var affected int64
	wg.Add(2)
	go func() {
		defer wg.Done()
		n, _, err := e.inbox.MarkThrough(ctx, e.buyer, newest)
		if err != nil {
			t.Error(err)
		}
		affected = n
	}()
	go func() { defer wg.Done(); e.notify(t, e.buyer, "effect-concurrent") }()
	wg.Wait()
	if affected != 25 {
		t.Fatalf("expected the 25 seen items marked, got %d", affected)
	}
	unread := e.page(t, e.buyer, true, "", 10).Items
	if len(unread) != 2 || e.unread(t, e.buyer) != 2 {
		t.Fatalf("the late notices must stay unread: %d items, count %d", len(unread), e.unread(t, e.buyer))
	}
	n, count, err := e.inbox.MarkThrough(ctx, e.buyer, newest)
	if err != nil || n != 0 || count != 2 {
		t.Fatalf("repeat read marker: %d %d %v", n, count, err)
	}
	if want := e.count(t, `SELECT count(*) FROM inbox_items WHERE recipient_id = $1 AND read_at IS NULL AND hidden_at IS NULL`, e.buyer); int64(want) != e.unread(t, e.buyer) {
		t.Fatal("count differs from the items")
	}
}

// Expired items are purged in batches; the delivery records stay. With
// the flag off no item is written and the API answers feature_disabled.
func TestInboxRetentionAndFlag(t *testing.T) {
	e := newInboxEnv(t)
	ctx := t.Context()
	for i := range 3 {
		e.notify(t, e.buyer, fmt.Sprintf("effect-old-%d", i))
	}
	e.notify(t, e.buyer, "effect-new")
	if _, err := e.pool.Exec(ctx, `UPDATE inbox_items SET created_at = now() - interval '91 days' WHERE event_id LIKE 'effect-old-%'`); err != nil {
		t.Fatal(err)
	}
	purged, err := e.inbox.PurgeExpired(ctx)
	if err != nil || purged != 3 {
		t.Fatalf("purged %d %v", purged, err)
	}
	if e.count(t, `SELECT count(*) FROM inbox_items`) != 1 || e.count(t, `SELECT count(*) FROM notifications`) != 4 {
		t.Fatal("purge must remove only expired inbox items")
	}

	plain := e.useCase(roles{}) // flag off: no inbox writer
	if _, _, err := plain.Accept(ctx, domain.Request{EventID: "effect-off", Source: "order", UserID: e.buyer, Type: domain.TypeOrderPaid, ReferenceID: "o"}); err != nil {
		t.Fatal(err)
	}
	if e.count(t, `SELECT count(*) FROM inbox_items WHERE event_id = 'effect-off'`) != 0 {
		t.Fatal("an item was written with the inbox off")
	}
	e.inbox.Enabled = false
	if _, err := e.inbox.UnreadCount(ctx, e.buyer); func() bool {
		var app *apperror.Error
		return !errors.As(err, &app) || app.Code != "feature_disabled" || app.Status != 404
	}() {
		t.Fatalf("expected feature_disabled, got %v", err)
	}
}

// Shop notices (AF-08) reach each recipient's inbox too; marketing consent
// is versioned and every change is audited.
func TestShopNoticesInInboxAndConsentAudit(t *testing.T) {
	s := newShopEnv(t)
	ctx := t.Context()
	repo := repository.InboxRepository{Pool: s.pool}
	uc := s.useCase(roles{})
	uc.Notifications.Inbox = repo
	if _, _, err := uc.Record(ctx, domain.VendorActionRequest{Source: "order", EventID: "effect-shop", VendorID: s.shop, ActionKind: "new_order", ReferenceID: "vo-1"}); err != nil {
		t.Fatal(err)
	}
	if found, err := uc.ResolveNext(ctx); err != nil || !found {
		t.Fatal(err)
	}
	if n := s.count(t, `SELECT count(*) FROM inbox_items WHERE recipient_id = $1 AND kind = 'vendor_new_order' AND link = '/vendor/orders'`, s.owner); n != 1 {
		t.Fatalf("owner inbox %d", n)
	}

	yes, no := true, false
	p, err := uc.UpdatePreferences(ctx, s.clerk, usecase.PreferenceChange{MarketingOptIn: &yes})
	if err != nil || !p.MarketingOptIn || p.Version != 1 {
		t.Fatalf("opt in: %+v %v", p, err)
	}
	if _, err := uc.UpdatePreferences(ctx, s.clerk, usecase.PreferenceChange{MarketingOptIn: &no}); err == nil {
		t.Fatal("a stale version must conflict")
	} else if app := (*apperror.Error)(nil); !errors.As(err, &app) || app.Code != "preference_version_conflict" {
		t.Fatalf("conflict code %v", err)
	}
	cats := []string{"orders"}
	if p, err = uc.UpdatePreferences(ctx, s.clerk, usecase.PreferenceChange{VendorCategories: &cats, ExpectedVersion: 1}); err != nil || !p.MarketingOptIn {
		t.Fatalf("changing categories must keep the consent: %+v %v", p, err)
	}
	if p, err = uc.UpdatePreferences(ctx, s.clerk, usecase.PreferenceChange{MarketingOptIn: &no, ExpectedVersion: 2}); err != nil || p.MarketingOptIn || len(p.Optional) != 1 {
		t.Fatalf("withdraw: %+v %v", p, err)
	}
	if s.count(t, `SELECT count(*) FROM notification_consent_audit WHERE user_id = $1`, s.clerk) != 2 ||
		s.count(t, `SELECT count(*) FROM notification_preferences WHERE user_id = $1 AND marketing_consented_at IS NOT NULL AND marketing_withdrawn_at IS NOT NULL`, s.clerk) != 1 {
		t.Fatal("consent changes must be audited with their times")
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM notification_consent_audit`); err == nil {
		t.Fatal("consent audit must be append-only")
	}
}
