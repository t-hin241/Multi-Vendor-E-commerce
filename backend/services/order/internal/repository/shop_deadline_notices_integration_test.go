package repository_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/casesla"
	"shopee/backend/pkg/config"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
	"shopee/backend/services/order/internal/usecase"
)

type anyAdmin struct{}

func (anyAdmin) RequireRole(context.Context, string, string) error { return nil }

type droppedNotices struct{}

func (droppedNotices) Publish(context.Context, eventbus.Envelope) error { return nil }

// slaWorker scans the order's deadlines with notices on (no on-call list:
// only the shop hook and the assignee are told).
func slaWorker(pool *pgxpool.Pool, uc *usecase.OrderUseCase) casesla.Worker {
	return casesla.Worker{Store: repository.NewCaseSLAStore(pool), Owner: "order", Config: config.CaseSLA{Enabled: true},
		Roles: anyAdmin{}, Publisher: droppedNotices{}, Log: zerolog.Nop(),
		WaitingOnShop: func(ctx context.Context, tx pgx.Tx, i *casesla.Item, kind string) error {
			return uc.NoticeShopDeadline(repository.WithTx(ctx, tx), i.Stage, i.ResourceID, i.DeadlineVersion, kind)
		}}
}

// moveDeadline puts a resource's reminder (and due time) at offsets from
// now and makes it due for the next scan.
func moveDeadline(t *testing.T, pool *pgxpool.Pool, resourceID, reminder, due string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE case_sla_work_items SET
		payload = jsonb_set(jsonb_set(payload, '{reminder_at}', to_jsonb(now() + $2::interval)), '{due_at}', to_jsonb(now() + $3::interval)),
		due_at = now() + $3::interval, next_check_at = now() - interval '1 second' WHERE resource_id = $1`, resourceID, reminder, due); err != nil {
		t.Fatal(err)
	}
}

func scan(t *testing.T, w casesla.Worker) {
	t.Helper()
	if err := w.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// PW-009 (AF-07): a deadline the shop must meet is told to the shop at its
// reminder and when it passes, once per deadline version; escalations stay
// with the marketplace.
func TestShopIsToldWhenItsDeadlineNearsAndPasses(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	uc, _ := holdUseCase(pool, "ok")
	uc.VendorActionNotices = true
	w := slaWorker(pool, uc)
	buyer, admin := uuid.NewString(), uuid.NewString()
	order, vo, vendor := paidSupportOrder(t, pool, buyer)
	sc, _, err := uc.CreateSupportCase(ctx, buyer, usecase.CreateSupportCaseInput{OrderID: order, VendorOrderID: vo, Category: "damaged", Message: "Hàng bị móp"})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := uc.AssignSupportCase(ctx, admin, sc.ID, admin, sc.Version, "Nhận xử lý")
	if err != nil {
		t.Fatal(err)
	}
	// The marketplace's own deadline is not the shop's.
	moveDeadline(t, pool, sc.ID, "-1 minute", "1 hour")
	scan(t, w)
	if n := countRowsIn(t, pool, `SELECT count(*) FROM order_effects WHERE kind = 'notify_vendor' AND target LIKE 'support_reply%'`); n != 0 {
		t.Fatalf("an admin stage is not told to the shop, got %d", n)
	}

	if _, err := uc.ChangeSupportCaseStatus(ctx, admin, sc.ID, string(domain.CaseWaitingVendor), assigned.Version, "Shop gửi ảnh đóng gói"); err != nil {
		t.Fatal(err)
	}
	moveDeadline(t, pool, sc.ID, "-1 minute", "1 hour")
	scan(t, w)
	moveDeadline(t, pool, sc.ID, "-1 minute", "1 hour")
	scan(t, w)
	targets := vendorNoticeTargets(t, pool, order)
	var due, overdue []domain.VendorNoticePayload
	for target, p := range targets {
		switch {
		case strings.HasPrefix(target, events.VendorActionSupportReplyOverdue+":"):
			overdue = append(overdue, p)
		case strings.HasPrefix(target, events.VendorActionSupportReplyDue+":"):
			due = append(due, p)
		}
	}
	if len(due) != 1 || len(overdue) != 0 || due[0].VendorID != vendor || due[0].ReferenceID != sc.ID || due[0].VendorOrderID != vo {
		t.Fatalf("one reminder to the shop of the case: %v", targets)
	}

	moveDeadline(t, pool, sc.ID, "-2 hours", "-1 minute")
	scan(t, w)
	moveDeadline(t, pool, sc.ID, "-2 hours", "-1 minute")
	scan(t, w)
	if n := countRowsIn(t, pool, `SELECT count(*) FROM order_effects WHERE kind = 'notify_vendor' AND target LIKE $1`, events.VendorActionSupportReplyOverdue+":"+sc.ID+":d%"); n != 1 {
		t.Fatalf("one overdue notice per deadline version, got %d", n)
	}
}

// PW-009 (AF-04 + AF-07): goods of a failed delivery the shop has not
// recorded are the shop's deadline too.
func TestShopIsToldWhenTheGoodsReceiptIsDue(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	f := newDeliveryFixture(t, pool)
	f.uc.VendorActionNotices = true
	if err := f.fact(ctx, f.shipment, domain.FactReturned, 1); err != nil {
		t.Fatal(err)
	}
	runDue(t, pool, f.uc)
	d := f.open(t)
	moveDeadline(t, pool, d.ID, "-1 minute", "1 hour")
	scan(t, slaWorker(pool, f.uc))
	if n := countRowsIn(t, pool, `SELECT count(*) FROM order_effects WHERE kind = 'notify_vendor' AND target LIKE $1`, events.VendorActionGoodsReceiptDue+":"+d.ID+":d%"); n != 1 {
		t.Fatalf("the shop is reminded to record the goods, got %d", n)
	}
}

type recordedNotifier struct {
	mu   sync.Mutex
	fail bool
	sent []string
}

func (r *recordedNotifier) Notify(_ context.Context, eventID, userID, notifType, referenceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return errors.New("notification down")
	}
	r.sent = append(r.sent, eventID+"|"+userID+"|"+notifType+"|"+referenceID)
	return nil
}

// PW-009: a support request without an order id is confirmed to the buyer
// when received and when closed, through Order's own outbox; a replay is
// not told twice and a failed relay is retried.
func TestBuyerIsToldAboutRequestsWithoutAnOrder(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	uc, _ := holdUseCase(pool, "ok")
	uc.BuyerNotices = repository.BuyerNotices{Pool: pool}
	notifier := &recordedNotifier{fail: true}
	uc.Notifications, uc.Events = notifier, nil
	buyer, admin := uuid.NewString(), uuid.NewString()
	in := usecase.SupportIntakeInput{ReferenceKind: "bank_transfer", Reference: "FAKE FT 0042", Message: "Tôi đã chuyển khoản nhưng không thấy đơn", IdempotencyKey: "intake-notice-1"}
	intake, _, err := uc.CreateSupportIntake(ctx, buyer, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, replayed, err := uc.CreateSupportIntake(ctx, buyer, in); err != nil || !replayed {
		t.Fatalf("replay: %v %v", replayed, err)
	}
	if n := countRowsIn(t, pool, `SELECT count(*) FROM order_buyer_notices WHERE reference_id = $1`, intake.ID); n != 1 {
		t.Fatalf("one received notice, got %d", n)
	}
	if _, err := uc.RelayBuyerNotices(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if n := countRowsIn(t, pool, `SELECT count(*) FROM order_buyer_notices WHERE reference_id = $1 AND delivered_at IS NULL AND attempts = 1`, intake.ID); n != 1 {
		t.Fatal("a failed relay is kept for a retry")
	}
	if _, err := uc.CloseSupportIntake(ctx, admin, intake.ID, intake.Version, "No payment matches this reference"); err != nil {
		t.Fatal(err)
	}
	notifier.fail = false
	if _, err := pool.Exec(ctx, `UPDATE order_buyer_notices SET next_attempt_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if n, err := uc.RelayBuyerNotices(ctx, 10); err != nil || n != 2 {
		t.Fatalf("relay: %d %v", n, err)
	}
	if n, err := uc.RelayBuyerNotices(ctx, 10); err != nil || n != 0 {
		t.Fatalf("a delivered notice is not sent again: %d %v", n, err)
	}
	got := map[string]bool{}
	for _, s := range notifier.sent {
		parts := strings.SplitN(s, "|", 4)
		id, user, typ, ref := parts[0], parts[1], parts[2], parts[3]
		if _, err := uuid.Parse(id); err != nil || user != buyer || ref != intake.ID {
			t.Fatalf("notice carries the row id, the buyer and the request: %s", s)
		}
		got[typ] = true
	}
	if !got["support_intake_received"] || !got["support_intake_closed"] {
		t.Fatalf("received and closed: %v", notifier.sent)
	}
}
