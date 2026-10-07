package repository_test

import (
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/adminaudit"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

func pendingOrder(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	order := uuid.NewString()
	if _, err := pool.Exec(t.Context(), `INSERT INTO orders(id,buyer_id,total_amount,subtotal_amount,currency,recipient_name,phone,province,district,ward,street_address)
		VALUES($1,$2,100,100,'VND','Test Recipient','0000000000','Test','Test','Test','Test street')`, order, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO vendor_orders(id,order_id,vendor_id,subtotal_amount,currency) VALUES($1,$2,$3,100,'VND')`,
		uuid.NewString(), order, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	return order
}

func orderStatus(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(t.Context(), `SELECT status FROM orders WHERE id=$1`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

// ADM-01: the audit row is part of the action's transaction. When it cannot
// be written the action is rolled back, and no audit claims a success.
func TestAdminCancel_AuditIsWrittenWithTheChangeOrNotAtAll(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	uc := realUseCase(pool, &inventoryReceiptStub{})
	admin := uuid.NewString()

	blocked := pendingOrder(t, pool)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER fail_audit BEFORE INSERT ON order_admin_audit FOR EACH ROW EXECUTE FUNCTION fail_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.AdminCancel(ctx, admin, blocked, "Buyer asked by phone"); err == nil {
		t.Fatal("cancel must fail when its audit cannot be written")
	}
	if got := orderStatus(t, pool, blocked); got != "pending_payment" {
		t.Fatalf("the cancel must roll back with its audit, order is %s", got)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_audit ON order_admin_audit`); err != nil {
		t.Fatal(err)
	}

	order := pendingOrder(t, pool)
	if _, err := uc.AdminCancel(middleware.ContextWithRequestID(ctx, "req-cancel-0001"), admin, order, "Buyer asked by phone"); err != nil {
		t.Fatal(err)
	}
	var actor, action, requestID, changes string
	if err := pool.QueryRow(ctx, `SELECT actor_id::text, action, request_id, changes::text FROM order_admin_audit WHERE entity_id=$1`, order).
		Scan(&actor, &action, &requestID, &changes); err != nil {
		t.Fatal(err)
	}
	if actor != admin || action != "order_cancelled" || requestID != "req-cancel-0001" || changes != `{"status": ["pending_payment", "cancelled"]}` {
		t.Fatalf("unexpected audit row: %s %s %s %s", actor, action, requestID, changes)
	}

	// Audit rows cannot be edited or removed afterwards.
	for _, stmt := range []string{`UPDATE order_admin_audit SET reason='x'`, `DELETE FROM order_admin_audit`} {
		if _, err := pool.Exec(ctx, stmt); err == nil {
			t.Fatalf("%s must be refused", stmt)
		}
	}

	// The admin audit search finds the row by request id, and pages by cursor.
	src := adminaudit.Source{Name: "order", SQL: repository.AuditSearchSQL, DB: pool, Roles: adminStub{}}
	q, _ := url.ParseQuery("request_id=req-cancel-0001")
	f, _ := adminaudit.ParseFilter(q)
	entries, err := src.Search(ctx, admin, f)
	if err != nil || len(entries) != 1 || entries[0].EntityID != order || entries[0].Source != "order" {
		t.Fatalf("search by request id: %+v %v", entries, err)
	}
	next := adminaudit.Filter{CursorTime: &entries[0].OccurredAt, CursorID: entries[0].ID, Limit: 10}
	if older, err := src.Search(ctx, admin, next); err != nil || len(older) != 0 {
		t.Fatalf("nothing is older than the only row: %+v %v", older, err)
	}
}

func TestOperationCountsRun(t *testing.T) {
	pool := orderDB(t)
	pendingOrder(t, pool)
	counts, err := (repository.Operations{Pool: pool}).Counts(t.Context())
	if err != nil || len(counts) != 8 || counts["awaiting_shipment"] != 0 || counts["support_cases_unassigned"] != 0 {
		t.Fatalf("unexpected counts %v %v", counts, err)
	}
}

// ADM-02: the idempotency key is enforced by the database too.
func TestRefundIdempotencyKeyIsUniquePerOrder(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	order := pendingOrder(t, pool)
	refunds := repository.NewRefundRepository(pool)
	key := "refund-key-0001"
	newRefund := func() *domain.Refund {
		return &domain.Refund{OrderID: order, ReasonCode: domain.RefundReasonDispute, Amount: 10, Currency: "VND", Reason: "test",
			RequestedBy: uuid.NewString(), IdempotencyKey: &key}
	}
	if err := refunds.Create(ctx, newRefund()); err != nil {
		t.Fatal(err)
	}
	if err := refunds.Create(ctx, newRefund()); err == nil {
		t.Fatal("a second refund with the same key must be refused")
	}
	found, err := refunds.FindByIdempotencyKey(ctx, order, key)
	if err != nil || found == nil || found.Amount != 10 {
		t.Fatalf("lookup by key: %+v %v", found, err)
	}
	if none, err := refunds.FindByIdempotencyKey(ctx, order, "other-key-0001"); err != nil || none != nil {
		t.Fatalf("unknown key: %+v %v", none, err)
	}
}
