package repository_test

import (
	"testing"

	"github.com/google/uuid"

	"shopee/backend/pkg/events"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

// PW-009: the shop hears about a support case on its package and each
// time the marketplace waits for its answer.
func TestShopIsToldAboutSupportCases(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	uc, _ := holdUseCase(pool, "ok")
	uc.VendorActionNotices = true
	buyer, admin := uuid.NewString(), uuid.NewString()
	order, vo, vendor := paidSupportOrder(t, pool, buyer)
	sc, _, err := uc.CreateSupportCase(ctx, buyer, usecase.CreateSupportCaseInput{OrderID: order, VendorOrderID: vo, Category: "damaged", Message: "Hàng bị móp"})
	if err != nil {
		t.Fatal(err)
	}
	targets := vendorNoticeTargets(t, pool, order)
	if p, ok := targets[events.VendorActionSupportCaseOpened+":"+sc.ID]; !ok || p.VendorID != vendor || p.VendorOrderID != vo {
		t.Fatalf("shop not told about the case: %v", targets)
	}
	assigned, err := uc.AssignSupportCase(ctx, admin, sc.ID, admin, sc.Version, "Nhận xử lý")
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := uc.ChangeSupportCaseStatus(ctx, admin, sc.ID, string(domain.CaseWaitingVendor), assigned.Version, "Shop gửi ảnh đóng gói")
	if err != nil {
		t.Fatal(err)
	}
	back, err := uc.ChangeSupportCaseStatus(ctx, admin, sc.ID, string(domain.CaseInProgress), waiting.Version, "Đã có ảnh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uc.ChangeSupportCaseStatus(ctx, admin, sc.ID, string(domain.CaseWaitingVendor), back.Version, "Thêm thông tin"); err != nil {
		t.Fatal(err)
	}
	if n := countRowsIn(t, pool, `SELECT count(*) FROM order_effects WHERE kind = 'notify_vendor' AND target LIKE $1`,
		events.VendorActionSupportWaitingShop+":"+sc.ID+":v%"); n != 2 {
		t.Fatalf("each time the case comes back to the shop it is told, got %d", n)
	}
}

// PW-009 (AF-04): a failed delivery coming back and the buyer accepting a
// redelivery are the shop's work.
func TestShopIsToldAboutReturnedGoodsAndRedelivery(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	f := newDeliveryFixture(t, pool)
	f.uc.VendorActionNotices = true
	if err := f.fact(ctx, f.shipment, domain.FactReturned, 1); err != nil {
		t.Fatal(err)
	}
	runDue(t, pool, f.uc)
	d := f.open(t)
	if _, ok := vendorNoticeTargets(t, pool, f.order)[events.VendorActionDeliveryGoodsReturned+":"+d.ID]; !ok {
		t.Fatal("shop not told that the goods are coming back")
	}
	d, err := f.receipt(ctx, "vendor", f.vendorUser, d, domain.ReceiptLine{OrderItemID: f.item, Condition: domain.ConditionSellable, Quantity: 2})
	if err != nil {
		t.Fatal(err)
	}
	d, err = f.uc.DecideDeliveryException(ctx, f.admin, d.ID, usecase.DeliveryDecision{Resolution: "redeliver", Reason: "Khách hẹn lại", ExpectedVersion: d.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.ConsentRedelivery(ctx, f.buyer, d.ID, usecase.ConsentInput{Accept: true, ExpectedVersion: d.Version}); err != nil {
		t.Fatal(err)
	}
	if p, ok := vendorNoticeTargets(t, pool, f.order)[events.VendorActionRedeliveryAccepted+":"+d.ID]; !ok || p.VendorID != f.vendor {
		t.Fatal("shop not told about the accepted redelivery")
	}
}

// PW-009 (AF-05): the buyer is reminded once before the dispatch deadline,
// and not at all once the parcel is reported sent.
func TestBuyerIsRemindedOnceBeforeTheDispatchDeadline(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	f := newReturnFixture(t, pool)
	f.dests.set(destination(1, "Hà Nội"))
	second := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO order_items(id,order_id,vendor_order_id,product_id,product_name,price_amount,quantity,subtotal_amount)
		VALUES($1,$2,$3,gen_random_uuid(),'Second product',50,1,50)`, second, f.order, f.vendorOrder); err != nil {
		t.Fatal(err)
	}
	approveOne := func(item, reason string) *domain.ReturnRequest {
		t.Helper()
		rr, err := f.uc.CreateReturn(ctx, f.buyer, usecase.ReturnInput{OrderID: f.order, ItemID: item, Quantity: 1, Reason: reason})
		if err != nil {
			t.Fatal(err)
		}
		if rr, err = f.uc.AdminDecideReturnWithTerms(ctx, f.admin, rr.ID, true, "Đồng ý", usecase.ReturnShippingTerms{}); err != nil {
			t.Fatal(err)
		}
		return rr
	}
	rr := approveOne(f.item, "Sai màu")
	if _, err := pool.Exec(ctx, `UPDATE return_requests SET dispatch_deadline = now() + interval '5 days' WHERE id = $1`, rr.ID); err != nil {
		t.Fatal(err)
	}
	reminders := func() int {
		return countRowsIn(t, pool, `SELECT count(*) FROM order_effects WHERE kind = 'notify' AND target = $1`, "return_dispatch_reminder:"+rr.ID)
	}
	f.uc.RemindReturnDispatch(ctx)
	if reminders() != 0 {
		t.Fatal("no reminder while the deadline is far")
	}
	if _, err := pool.Exec(ctx, `UPDATE return_requests SET dispatch_deadline = now() + interval '1 day' WHERE id = $1`, rr.ID); err != nil {
		t.Fatal(err)
	}
	f.uc.RemindReturnDispatch(ctx)
	f.uc.RemindReturnDispatch(ctx)
	if n := reminders(); n != 1 {
		t.Fatalf("expected one reminder, got %d", n)
	}

	sent := approveOne(second, "Không vừa")
	if _, err := pool.Exec(ctx, `UPDATE return_requests SET dispatch_deadline = now() + interval '1 day' WHERE id = $1`, sent.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.dispatch(ctx, f.load(t, sent.ID), "reminder-key-1", "VN987654321"); err != nil {
		t.Fatal(err)
	}
	f.uc.RemindReturnDispatch(ctx)
	if n := countRowsIn(t, pool, `SELECT count(*) FROM order_effects WHERE kind = 'notify' AND target = $1`, "return_dispatch_reminder:"+sent.ID); n != 0 {
		t.Fatal("a sent parcel needs no reminder")
	}
}
