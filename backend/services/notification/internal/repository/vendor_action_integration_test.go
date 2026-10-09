package repository_test

import (
	"context"
	"errors"
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

// directory fakes Vendor's recipient list; down makes it unavailable and
// missing answers 404 (shop gone).
type directory struct {
	mu      sync.Mutex
	list    []adapter.NoticeRecipient
	version string
	down    bool
	missing bool
	calls   int
}

func (d *directory) NotificationRecipients(_ context.Context, _, _ string) (*adapter.NoticeRecipients, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	switch {
	case d.down:
		return nil, apperror.Internal(errors.New("vendor service returned status 503"))
	case d.missing:
		return nil, apperror.NotFound("Shop not found")
	}
	return &adapter.NoticeRecipients{Recipients: append([]adapter.NoticeRecipient{}, d.list...), PermissionVersion: d.version}, nil
}

type shopEnv struct {
	*env
	actions repository.VendorActionRepository
	dir     *directory
	shop    string
	owner   string
	clerk   string
	other   string
}

func newShopEnv(t *testing.T) *shopEnv {
	e := &shopEnv{env: newEnv(t), shop: uuid.NewString(), owner: uuid.NewString(), clerk: uuid.NewString(), other: uuid.NewString()}
	e.actions = repository.VendorActionRepository{Pool: e.pool}
	for _, u := range []string{e.owner, e.clerk, e.other} {
		e.ids.users[u] = &adapter.UserSnapshot{ID: u, Email: u[:6] + "@example.invalid", FullName: "Shop member", Active: true}
	}
	e.dir = &directory{version: "v1", list: []adapter.NoticeRecipient{{UserID: e.owner, Role: "owner"}, {UserID: e.clerk, Role: "staff"},
		{UserID: e.other, Role: "staff"}}}
	return e
}

func (e *shopEnv) useCase(r roles) *usecase.VendorActionUseCase {
	return &usecase.VendorActionUseCase{Store: e.actions, Notifications: e.env.useCase(r), Directory: e.dir,
		Tx: repository.Transactions{Pool: e.pool}, Enabled: true, Log: zerolog.Nop()}
}

func (e *shopEnv) record(t *testing.T, eventID, kind string) *domain.VendorAction {
	t.Helper()
	vo := uuid.NewString()
	a, _, err := e.useCase(roles{}).Record(t.Context(), domain.VendorActionRequest{Source: "order", EventID: eventID, VendorID: e.shop,
		ActionKind: kind, ReferenceID: vo, VendorOrderID: vo})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (e *shopEnv) resolve(t *testing.T) bool {
	t.Helper()
	found, err := e.useCase(roles{}).ResolveNext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func (e *shopEnv) action(t *testing.T, id string) *domain.VendorAction {
	t.Helper()
	a, err := e.actions.Find(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// A repeated event is recorded once; it reaches the owner and the staff
// who opted into its category, once each, and is delivered by the normal
// queue.
func TestVendorActionReachesOwnerAndOptedInStaffOnce(t *testing.T) {
	e := newShopEnv(t)
	uc := e.useCase(roles{})
	if _, err := uc.UpdatePreferences(t.Context(), e.clerk, categories(0, []string{"orders"})); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.UpdatePreferences(t.Context(), e.other, categories(0, []string{"finance"})); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := uc.Record(t.Context(), domain.VendorActionRequest{Source: "order", EventID: "effect-new-order", VendorID: e.shop,
				ActionKind: "new_order", ReferenceID: "vo-1"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := e.count(t, `SELECT count(*) FROM vendor_action_events`); n != 1 {
		t.Fatalf("expected one event, got %d", n)
	}
	if e.jobs.size() != 0 || e.count(t, `SELECT count(*) FROM notifications`) != 0 {
		t.Fatal("recording must not resolve or send")
	}
	if !e.resolve(t) || e.resolve(t) {
		t.Fatal("expected exactly one due event")
	}
	got := e.count(t, `SELECT count(*) FROM notifications WHERE type = 'vendor_new_order' AND user_id IN ($1, $2)`, e.owner, e.clerk)
	if got != 2 || e.count(t, `SELECT count(*) FROM notifications`) != 2 {
		t.Fatalf("expected the owner and the opted-in clerk, got %d of %d", got, e.count(t, `SELECT count(*) FROM notifications`))
	}
	var a *domain.VendorAction
	if items, _, err := e.actions.List(t.Context(), repository.VendorActionFilter{}, 10, 0); err != nil || len(items) != 1 {
		t.Fatal(err)
	} else {
		a = items[0]
	}
	if a.Status != domain.VendorActionResolved || len(a.RecipientUserIDs) != 2 || a.PermissionVersion == nil || *a.PermissionVersion != "v1" {
		t.Fatalf("resolution not recorded: %+v", a)
	}
	if e.run(t) != 2 || e.mail.count() != 2 {
		t.Fatalf("expected two emails, got %d", e.mail.count())
	}

	// A later change in Vendor does not alter a resolved event.
	e.dir.list = nil
	if e.resolve(t) {
		t.Fatal("a resolved event must not be resolved again")
	}
}

// Vendor unavailable: the event survives, is retried with backoff and is
// parked after its last attempt; an admin retry (reason, audited) resolves
// it once Vendor answers. A shop nobody may receive for waits for review.
func TestVendorActionRetriesParksAndNeedsReview(t *testing.T) {
	e := newShopEnv(t)
	a := e.record(t, "effect-outage", "return_requested")
	e.dir.down = true
	for i := 1; i <= domain.MaxVendorActionAttempts; i++ {
		if _, err := e.pool.Exec(t.Context(), `UPDATE vendor_action_events SET next_attempt_at = now() WHERE id = $1`, a.ID); err != nil {
			t.Fatal(err)
		}
		if !e.resolve(t) {
			t.Fatalf("attempt %d found nothing", i)
		}
		got := e.action(t, a.ID)
		want := domain.VendorActionPending
		if i == domain.MaxVendorActionAttempts {
			want = domain.VendorActionParked
		}
		if got.Status != want || got.Attempts != i || (want == domain.VendorActionPending && !got.NextAttemptAt.After(time.Now())) {
			t.Fatalf("attempt %d: %s/%d next %v", i, got.Status, got.Attempts, got.NextAttemptAt)
		}
	}
	if e.count(t, `SELECT count(*) FROM notifications`) != 0 {
		t.Fatal("nothing may be sent without a recipient list")
	}

	e.dir.down = false
	if _, err := e.useCase(roles{}).Retry(t.Context(), uuid.NewString(), a.ID, ""); err == nil {
		t.Fatal("retry without a reason accepted")
	}
	if _, err := e.useCase(roles{deny: true}).Retry(t.Context(), uuid.NewString(), a.ID, "vendor is back"); err == nil {
		t.Fatal("retry by a non-admin accepted")
	}
	admin := uuid.NewString()
	if _, err := e.useCase(roles{}).Retry(t.Context(), admin, a.ID, "vendor is back"); err != nil {
		t.Fatal(err)
	}
	if !e.resolve(t) || e.action(t, a.ID).Status != domain.VendorActionResolved {
		t.Fatal("retried event not resolved")
	}
	if e.count(t, `SELECT count(*) FROM notifications WHERE user_id = $1 AND type = 'vendor_return_requested'`, e.owner) != 1 {
		t.Fatal("owner not told after the retry")
	}
	if e.count(t, `SELECT count(*) FROM notification_admin_audit WHERE entity_type = 'vendor_action_event' AND actor_id = $1`, admin) != 1 {
		t.Fatal("retry not audited")
	}
	if _, err := e.useCase(roles{}).Retry(t.Context(), admin, a.ID, "again"); err == nil {
		t.Fatal("a resolved event cannot be retried")
	}

	// Owner locked (Vendor leaves them out) and no staff opted in.
	e.dir.list = []adapter.NoticeRecipient{{UserID: e.clerk, Role: "staff"}}
	locked := e.record(t, "effect-locked", "new_order")
	e.resolve(t)
	if got := e.action(t, locked.ID); got.Status != domain.VendorActionNoRecipient || got.LastError == nil {
		t.Fatalf("expected no_recipient, got %s", got.Status)
	}
	e.dir.missing = true
	gone := e.record(t, "effect-gone", "new_order")
	e.resolve(t)
	if e.action(t, gone.ID).Status != domain.VendorActionNoRecipient {
		t.Fatal("a missing shop must wait for review")
	}
	counts, err := e.actions.Counts(t.Context())
	if err != nil || counts["vendor_actions_no_recipient"] != 2 {
		t.Fatalf("counts %v %v", counts, err)
	}
	if e.count(t, `SELECT count(*) FROM notifications WHERE reference_id IN ($1, $2)`, locked.ReferenceID, gone.ReferenceID) != 0 {
		t.Fatal("notices written for an event without recipients")
	}
}

// A worker that stopped mid-resolution loses the event when its lease
// ends; its late result is refused, so the notices are written once.
func TestVendorActionStoppedWorkerCannotDuplicate(t *testing.T) {
	e := newShopEnv(t)
	a := e.record(t, "effect-crash", "new_order")
	held, err := e.actions.Claim(t.Context(), 30*time.Second)
	if err != nil || held.ID != a.ID {
		t.Fatalf("claim: %v", err)
	}
	if e.resolve(t) {
		t.Fatal("a held event must not be taken by another worker")
	}
	if _, err := e.pool.Exec(t.Context(), `UPDATE vendor_action_events SET lease_until = now() - interval '1 second' WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if !e.resolve(t) || e.action(t, a.ID).Status != domain.VendorActionResolved {
		t.Fatal("expired lease not taken over")
	}
	err = e.actions.Complete(t.Context(), held.ID, held.Attempts, repository.Resolution{Status: domain.VendorActionResolved,
		RecipientUserIDs: []string{e.other}, NextAttemptAt: time.Now()})
	if !errors.Is(err, repository.ErrStale) {
		t.Fatalf("late result accepted: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM notifications`); n != 1 {
		t.Fatalf("expected one notice (owner), got %d", n)
	}
}

func categories(version int64, cats []string) usecase.PreferenceChange {
	return usecase.PreferenceChange{VendorCategories: &cats, ExpectedVersion: version}
}

// Preferences: self-service, versioned, validated; off with the flag.
func TestNoticePreferences(t *testing.T) {
	e := newShopEnv(t)
	uc := e.useCase(roles{})
	p, err := uc.GetPreferences(t.Context(), e.clerk)
	if err != nil || len(p.Optional) != 0 || p.Version != 0 {
		t.Fatalf("default preferences: %+v %v", p, err)
	}
	if p, err = uc.UpdatePreferences(t.Context(), e.clerk, categories(0, []string{"returns", "orders"})); err != nil || p.Version != 1 {
		t.Fatalf("first save: %+v %v", p, err)
	}
	if _, err := uc.UpdatePreferences(t.Context(), e.clerk, categories(0, []string{"finance"})); err == nil {
		t.Fatal("a stale first save must conflict")
	}
	if p, err = uc.UpdatePreferences(t.Context(), e.clerk, categories(1, []string{})); err != nil || p.Version != 2 || len(p.Optional) != 0 {
		t.Fatalf("clear: %+v %v", p, err)
	}
	if _, err := uc.UpdatePreferences(t.Context(), e.clerk, categories(2, []string{"marketing"})); err == nil {
		t.Fatal("unknown category accepted")
	}
	uc.Enabled = false
	if _, err := uc.GetPreferences(t.Context(), e.clerk); err == nil {
		t.Fatal("preferences answered while the feature is off")
	}
	if _, _, err := uc.Record(t.Context(), domain.VendorActionRequest{Source: "order", EventID: "e", VendorID: e.shop, ActionKind: "new_order", ReferenceID: "r"}); err == nil {
		t.Fatal("event recorded while the feature is off")
	}
}

// PW-009: support notices use their own purpose (support.reply at Vendor)
// and staff may opt into the category.
func TestSupportNoticesHaveTheirOwnCategory(t *testing.T) {
	e := newShopEnv(t)
	uc := e.useCase(roles{})
	cats := []string{"support"}
	if _, err := uc.UpdatePreferences(t.Context(), e.clerk, usecase.PreferenceChange{VendorCategories: &cats}); err != nil {
		t.Fatal(err)
	}
	a, _, err := uc.Record(t.Context(), domain.VendorActionRequest{Source: "order", EventID: "effect-support", VendorID: e.shop,
		ActionKind: "support_case_opened", ReferenceID: uuid.NewString()})
	if err != nil || a.Purpose != domain.PurposeSupport {
		t.Fatalf("support notice: %+v %v", a, err)
	}
	e.resolve(t)
	if n := e.count(t, `SELECT count(*) FROM notifications WHERE type = 'vendor_support_case_opened' AND user_id IN ($1, $2)`, e.owner, e.clerk); n != 2 {
		t.Fatalf("owner and the opted-in clerk, got %d", n)
	}
}
