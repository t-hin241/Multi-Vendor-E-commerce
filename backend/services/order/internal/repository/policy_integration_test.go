package repository_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// AF-02: the read model keeps every published version once and picks the
// one in force by effective_at; a placed order stores its snapshot with
// the order and keeps it whatever is published later.
func TestPolicyReadModelAndOrderSnapshot(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	policies := repository.NewPolicyVersionRepository(pool)
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	v1 := domain.PolicyVersion{PolicyID: uuid.NewString(), Scope: "marketplace", Kind: "returns", Version: 1, ContentHash: "h1",
		RuleRefs: map[string]string{domain.RuleReturnsWindow: "window-14d", domain.RuleReturnShippingRefund: "none"}, EffectiveAt: at.Add(-time.Hour)}
	v2 := v1
	v2.PolicyID, v2.Version, v2.ContentHash, v2.EffectiveAt = uuid.NewString(), 2, "h2", at
	v2.RuleRefs = map[string]string{domain.RuleReturnsWindow: "window-3d", domain.RuleReturnShippingRefund: "none"}
	vendor := uuid.NewString()
	shop := domain.PolicyVersion{PolicyID: uuid.NewString(), Scope: "shop", VendorID: vendor, Kind: "shop", Version: 1, ContentHash: "hs",
		RuleRefs: map[string]string{}, EffectiveAt: at.Add(-time.Hour)}
	for _, v := range []domain.PolicyVersion{v1, v2, shop} {
		if inserted, err := policies.Insert(ctx, v); err != nil || !inserted {
			t.Fatalf("insert: %v %v", inserted, err)
		}
	}
	if inserted, err := policies.Insert(ctx, v1); err != nil || inserted {
		t.Fatalf("a replay is a no-op: %v %v", inserted, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE policy_versions SET content_hash = 'x' WHERE policy_id = $1`, v1.PolicyID); err == nil {
		t.Fatal("read model rows are immutable")
	}

	before, err := policies.MarketplaceAt(ctx, at.Add(-time.Nanosecond))
	if err != nil || len(before) != 1 || before[0].Version != 1 {
		t.Fatalf("v1 in force just before the boundary: %v %+v", err, before)
	}
	atBoundary, _ := policies.MarketplaceAt(ctx, at)
	if len(atBoundary) != 1 || atBoundary[0].Version != 2 {
		t.Fatalf("v2 in force at the boundary: %+v", atBoundary)
	}
	shops, err := policies.ShopsAt(ctx, []string{vendor, uuid.NewString()}, at)
	if err != nil || len(shops) != 1 || shops[vendor].PolicyID != shop.PolicyID {
		t.Fatalf("shop policy lookup: %v %+v", err, shops)
	}

	snapshot, err := domain.BuildPolicySnapshot(domain.ActiveAt(before, at.Add(-time.Nanosecond)), domain.ReturnPolicy{Version: "window-7d", WindowDays: 7}, at)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := domain.BuildCheckoutPlan(uuid.NewString(), []domain.CheckoutLine{{ProductID: uuid.NewString(), VendorID: vendor, ProductName: "P",
		PriceAmount: 100, Currency: "VND", Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	plan.Order.RecipientName, plan.Order.Phone, plan.Order.Province, plan.Order.District, plan.Order.Ward, plan.Order.StreetAddress =
		"Test Recipient", "0000000000", "Test", "Test", "Test", "Test street"
	plan.VendorVersions = map[string]int64{vendor: 1}
	plan.ProductVersions = map[string]int64{plan.Items[0].ProductID: 1}
	if _, err := pool.Exec(ctx, `INSERT INTO vendor_sale_status (vendor_id, version, status) VALUES ($1, 1, 'approved')`, vendor); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_sale_status (product_id, version, is_visible) VALUES ($1, 1, true)`, plan.Items[0].ProductID); err != nil {
		t.Fatal(err)
	}
	plan.OrderPolicy = snapshot
	plan.VendorPolicies = []*domain.VendorPolicySnapshot{snapshot.ForVendor(&shop)}
	order, err := repository.NewOrderRepository(pool).CreateFromPlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	stored, vendors, err := policies.OrderSnapshot(ctx, order.ID)
	if err != nil || stored == nil || stored.ReturnsWindowDays != 14 || len(vendors) != 1 {
		t.Fatalf("the order keeps its snapshot: %v %+v %v", err, stored, vendors)
	}
	for id, v := range vendors {
		single, err := policies.VendorSnapshot(ctx, id)
		if err != nil || single.ShopPolicy == nil || single.ShopPolicy.PolicyID != shop.PolicyID || v.ReturnPolicy().WindowDays != 14 {
			t.Fatalf("vendor order snapshot: %v %+v", err, single)
		}
	}
	legacy := pendingOrder(t, pool)
	if s, _, err := policies.OrderSnapshot(ctx, legacy); err != nil || s != nil {
		t.Fatalf("an order placed before snapshots has none: %v %+v", err, s)
	}
}
