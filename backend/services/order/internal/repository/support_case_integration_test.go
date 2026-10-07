package repository_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
	"shopee/backend/services/order/internal/usecase"
)

// paidSupportOrder inserts a paid order of buyer with one vendor order.
func paidSupportOrder(t *testing.T, pool *pgxpool.Pool, buyer string) (order, vendorOrder, vendor string) {
	t.Helper()
	order, vendorOrder, vendor = uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(t.Context(), `INSERT INTO orders(id,buyer_id,status,total_amount,subtotal_amount,currency,recipient_name,phone,province,district,ward,street_address)
		VALUES($1,$2,'paid',100,100,'VND','Test Recipient','0000000000','Test','Test','Test','Test street')`, order, buyer); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO vendor_orders(id,order_id,vendor_id,status,subtotal_amount,currency) VALUES($1,$2,$3,'paid',100,'VND')`,
		vendorOrder, order, vendor); err != nil {
		t.Fatal(err)
	}
	return order, vendorOrder, vendor
}

func supportUseCase(pool *pgxpool.Pool) *usecase.OrderUseCase {
	uc := realUseCase(pool, &inventoryReceiptStub{})
	uc.Support = repository.NewSupportCaseRepository(pool)
	uc.SupportConfig.Enabled = true
	return uc
}

// AF-01: a case is written with its first message, timeline and buyer
// notice in one transaction; a money case holds the vendor order's payout
// until it is closed; the unique index allows one case not yet closed per
// topic even under concurrent requests.
func TestSupportCase_HoldsPayoutUntilClosedAndOneOpenCasePerTopic(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	uc := supportUseCase(pool)
	buyer := uuid.NewString()
	order, vo, _ := paidSupportOrder(t, pool, buyer)

	var wg sync.WaitGroup
	results := make([]error, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, results[i] = uc.CreateSupportCase(ctx, buyer, usecase.CreateSupportCaseInput{OrderID: order, VendorOrderID: vo,
				Category: "not_received", Message: "Chưa nhận được hàng"})
		}()
	}
	wg.Wait()
	created := 0
	for _, err := range results {
		var app *apperror.Error
		switch {
		case err == nil:
			created++
		case !errors.As(err, &app) || app.Code != domain.CodeCaseAlreadyOpen:
			t.Fatalf("unexpected error %v", err)
		}
	}
	if created != 1 {
		t.Fatalf("exactly one case per topic, got %d", created)
	}
	page, err := uc.ListMySupportCases(ctx, buyer, "", "", 10)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("list: %v %+v", err, page)
	}
	sc := page.Items[0]
	var messages, events, notices int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM support_messages WHERE case_id=$1), (SELECT count(*) FROM support_case_history WHERE case_id=$1),
		(SELECT count(*) FROM order_effects WHERE order_id=$2 AND kind='notify' AND target LIKE 'support_case_opened:%')`, sc.ID, order).
		Scan(&messages, &events, &notices); err != nil {
		t.Fatal(err)
	}
	if messages != 1 || events != 1 || notices != 1 {
		t.Fatalf("case, message, timeline and notice go together: %d %d %d", messages, events, notices)
	}

	held, err := repository.NewVendorOrderRepository(pool).HeldForSettlement(ctx, []string{vo})
	if err != nil || held[vo] != "support_case_open" {
		t.Fatalf("an open money case holds the payout, got %v %v", held, err)
	}

	admin := uuid.NewString()
	assigned, err := uc.AssignSupportCase(ctx, admin, sc.ID, admin, sc.Version, "Take responsibility")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uc.AssignSupportCase(ctx, admin, sc.ID, admin, sc.Version, "Take responsibility"); err == nil {
		t.Fatal("a stale expected_version must be refused")
	}
	resolved, _, err := uc.ResolveSupportCase(ctx, admin, sc.ID, usecase.ResolveInput{Kind: domain.ResolutionNoAction, Reason: "Đã giao",
		ExpectedVersion: assigned.Version})
	if err != nil {
		t.Fatal(err)
	}
	if held, _ := repository.NewVendorOrderRepository(pool).HeldForSettlement(ctx, []string{vo}); held[vo] == "" {
		t.Fatal("a resolved case can still be reopened: the hold stays until it is closed")
	}
	if _, err := uc.CloseSupportCase(ctx, admin, sc.ID, resolved.Version, "Buyer confirmed by phone"); err != nil {
		t.Fatal(err)
	}
	if held, _ := repository.NewVendorOrderRepository(pool).HeldForSettlement(ctx, []string{vo}); len(held) != 0 {
		t.Fatalf("a closed case releases the hold, got %v", held)
	}
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM order_admin_audit WHERE entity_type='support_case' AND entity_id=$1`, sc.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 3 {
		t.Fatalf("assign, resolve and close are audited, got %d", audits)
	}

	// A new case on the same topic is allowed once the first is closed.
	if _, _, err := uc.CreateSupportCase(ctx, buyer, usecase.CreateSupportCaseInput{OrderID: order, VendorOrderID: vo,
		Category: "not_received", Message: "Lại chưa nhận", RelatedCaseID: sc.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE support_messages SET text='edited' WHERE case_id=$1`, sc.ID); err == nil {
		t.Fatal("messages are append-only")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM support_case_history WHERE case_id=$1`, sc.ID); err == nil {
		t.Fatal("history is append-only")
	}
}

func TestSupportCase_InternalNotesAndCursorInSQL(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	uc := supportUseCase(pool)
	buyer, admin := uuid.NewString(), uuid.NewString()
	order, vo, vendor := paidSupportOrder(t, pool, buyer)
	var ids []string
	for _, category := range []string{"not_received", "damaged", "wrong_items"} {
		sc, _, err := uc.CreateSupportCase(ctx, buyer, usecase.CreateSupportCaseInput{OrderID: order, VendorOrderID: vo, Category: category, Message: "x"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, sc.ID)
	}
	if _, _, err := uc.PostSupportMessage(ctx, usecase.SupportActor{ID: admin, Role: "admin"}, ids[0],
		usecase.SupportMessageInput{Text: "ghi chú nội bộ", Visibility: domain.VisibilityInternal}); err != nil {
		t.Fatal(err)
	}
	support := repository.NewSupportCaseRepository(pool)
	public, err := support.ListMessages(ctx, ids[0], false)
	if err != nil || len(public) != 1 {
		t.Fatalf("buyers and vendors get public messages only, got %v %d", err, len(public))
	}
	all, _ := support.ListMessages(ctx, ids[0], true)
	if len(all) != 2 {
		t.Fatalf("admins get the internal note too, got %d", len(all))
	}

	first, err := support.List(ctx, repository.SupportCaseFilter{VendorID: vendor}, nil, 2)
	if err != nil || len(first) != 2 {
		t.Fatalf("got %v %d", err, len(first))
	}
	rest, err := support.List(ctx, repository.SupportCaseFilter{VendorID: vendor}, &repository.CaseCursor{CreatedAt: first[1].CreatedAt, ID: first[1].ID}, 2)
	if err != nil || len(rest) != 1 || rest[0].ID == first[0].ID || rest[0].ID == first[1].ID {
		t.Fatalf("the cursor continues after the last case, got %v %+v", err, rest)
	}
	other, _ := support.List(context.Background(), repository.SupportCaseFilter{VendorID: uuid.NewString()}, nil, 10)
	if len(other) != 0 {
		t.Fatal("another shop sees nothing")
	}
}
