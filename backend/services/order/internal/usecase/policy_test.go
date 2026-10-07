package usecase_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

// fakePolicyVersions is Order's policy read model and order snapshots.
type fakePolicyVersions struct {
	mu       sync.Mutex
	versions map[string]domain.PolicyVersion
	orders   map[string]*domain.OrderPolicySnapshot
	vendors  map[string]*domain.VendorPolicySnapshot
	byOrder  map[string][]string
}

func newFakePolicyVersions() *fakePolicyVersions {
	return &fakePolicyVersions{versions: map[string]domain.PolicyVersion{}, orders: map[string]*domain.OrderPolicySnapshot{},
		vendors: map[string]*domain.VendorPolicySnapshot{}, byOrder: map[string][]string{}}
}

func (f *fakePolicyVersions) storeSnapshot(orderID string, o *domain.OrderPolicySnapshot, vendorOrderIDs []string, v []*domain.VendorPolicySnapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orders[orderID] = o
	for i, id := range vendorOrderIDs {
		f.vendors[id] = v[i]
		f.byOrder[orderID] = append(f.byOrder[orderID], id)
	}
}

func (f *fakePolicyVersions) Insert(_ context.Context, v domain.PolicyVersion) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.versions[v.PolicyID]; ok {
		return false, nil
	}
	f.versions[v.PolicyID] = v
	return true, nil
}

func (f *fakePolicyVersions) Find(_ context.Context, id string) (*domain.PolicyVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.versions[id]; ok {
		return &v, nil
	}
	return nil, nil
}

func (f *fakePolicyVersions) MarketplaceAt(_ context.Context, t time.Time) ([]domain.PolicyVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []domain.PolicyVersion{}
	for _, v := range f.versions {
		if v.Scope == "marketplace" {
			out = append(out, v)
		}
	}
	return out, nil
}

func (f *fakePolicyVersions) ShopsAt(_ context.Context, vendorIDs []string, t time.Time) (map[string]domain.PolicyVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]domain.PolicyVersion{}
	for _, v := range f.versions {
		if v.Scope == "shop" && !v.EffectiveAt.After(t) {
			if cur, ok := out[v.VendorID]; !ok || v.EffectiveAt.After(cur.EffectiveAt) {
				out[v.VendorID] = v
			}
		}
	}
	return out, nil
}

func (f *fakePolicyVersions) OrderSnapshot(_ context.Context, orderID string) (*domain.OrderPolicySnapshot, map[string]*domain.VendorPolicySnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	vendors := map[string]*domain.VendorPolicySnapshot{}
	for _, id := range f.byOrder[orderID] {
		vendors[id] = f.vendors[id]
	}
	return f.orders[orderID], vendors, nil
}

func (f *fakePolicyVersions) VendorSnapshot(_ context.Context, vendorOrderID string) (*domain.VendorPolicySnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.vendors[vendorOrderID], nil
}

func publishReturns(t *testing.T, f *checkoutFixture, id string, version int64, window string, at time.Time) {
	t.Helper()
	err := f.uc.ApplyPolicyPublished(t.Context(), domain.PolicyVersion{PolicyID: id, Scope: "marketplace", Kind: "returns", Version: version,
		ContentHash: "hash-" + id, EffectiveAt: at,
		RuleRefs: map[string]string{domain.RuleReturnsWindow: window, domain.RuleReturnShippingRefund: "none"}})
	if err != nil {
		t.Fatal(err)
	}
}

// AF-02: an order keeps the policy in force when it was placed; a later
// version applies to later orders only, for returns and for the payout date.
func TestPolicySnapshot_OrdersKeepThePolicyTheyWerePlacedUnder(t *testing.T) {
	f := newCheckoutFixture()
	f.uc.VersionedPolicies = true
	start := f.now
	publishReturns(t, f, "11111111-0000-0000-0000-000000000001", 1, "window-14d", start.Add(-time.Hour))

	if _, err := f.uc.Preview(t.Context(), "buyer-1", f.addressID); err == nil {
		t.Fatal("empty cart")
	}
	f.product("p1", "vendor-a", 100000)
	f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 1})
	preview, err := f.uc.Preview(t.Context(), "buyer-1", f.addressID)
	if err != nil || preview.Policies == nil ||
		preview.Policies.VersionsByKind()["returns"] != 1 || preview.Policies.ReturnsWindowDays != 14 {
		t.Fatalf("the preview shows the versions in force: %v %+v", err, preview)
	}
	order, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "policy-order-1",
		AcceptedPolicyVersions: preview.Policies.VersionsByKind()})
	if err != nil {
		t.Fatal(err)
	}

	// v2 shortens the window; it must not touch the order placed under v1.
	publishReturns(t, f, "11111111-0000-0000-0000-000000000002", 2, "window-3d", start)
	f.pay(t, order, "77777777-7777-7777-7777-777777777777")
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	vos, _ := f.vendorOrders.ListByOrderID(t.Context(), order.ID)
	if _, err := f.uc.UpdateVendorOrderStatus(t.Context(), "user-a", vos[0].ID, domain.StatusProcessing); err != nil {
		t.Fatal(err)
	}
	shipmentEvent(t, f, vos[0].ID, "delivered")
	completed, _ := f.vendorOrders.FindByID(t.Context(), vos[0].ID)

	f.now = completed.CompletedAt.Add(10 * 24 * time.Hour)
	rr, err := f.uc.CreateReturn(t.Context(), "buyer-1", usecase.ReturnInput{OrderID: order.ID, ItemID: itemOf(f, completed).ID, Reason: "Hỏng"})
	if err != nil {
		t.Fatalf("day 10 is inside the 14-day window the order was sold under: %v", err)
	}
	if rr.PolicyVersion != "returns-v1" || rr.ReturnWindowDays == nil || *rr.ReturnWindowDays != 14 {
		t.Fatalf("the return records the order's policy, got %s %v", rr.PolicyVersion, rr.ReturnWindowDays)
	}
	if n := len(f.payment.settlements); n != 1 || !f.payment.settlements[0].EligibleAt.Equal(completed.CompletedAt.UTC().Add(14*24*time.Hour)) {
		t.Fatalf("the payout waits for the order's own return window, got %+v", f.payment.settlements)
	}

	view, err := f.uc.PolicySnapshot(t.Context(), "buyer-1", "buyer", order.ID)
	if err != nil || view.Legacy || view.Order.ReturnPolicyVersion != "returns-v1" || view.VendorOrders[vos[0].ID] == nil {
		t.Fatalf("the buyer reads the order's snapshot: %v %+v", err, view)
	}
	_, err = f.uc.PolicySnapshot(t.Context(), "buyer-2", "buyer", order.ID)
	expectCode(t, err, apperror.CodeNotFound)
	_, err = f.uc.PolicySnapshot(t.Context(), "user-b", "vendor", order.ID)
	expectCode(t, err, apperror.CodeNotFound)
	if v, err := f.uc.PolicySnapshot(t.Context(), "user-a", "vendor", order.ID); err != nil || len(v.VendorOrders) != 1 {
		t.Fatalf("the selling shop reads its vendor order's snapshot: %v", err)
	}
}

func TestPolicySnapshot_StaleAcceptanceIsRefused(t *testing.T) {
	f := newCheckoutFixture()
	f.uc.VersionedPolicies = true
	publishReturns(t, f, "22222222-0000-0000-0000-000000000001", 1, "window-7d", f.now.Add(-time.Hour))
	f.product("p1", "vendor-a", 100000)
	f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 1})

	_, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "policy-stale-1",
		AcceptedPolicyVersions: map[string]int64{}})
	expectCode(t, err, domain.CodePolicyChanged)
	_, _, err = f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "policy-stale-2",
		AcceptedPolicyVersions: map[string]int64{"returns": 2}})
	expectCode(t, err, domain.CodePolicyChanged)
	if len(f.orders.byID) != 0 {
		t.Fatal("no order is created when the buyer saw other policies")
	}
	order, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "policy-stale-3",
		AcceptedPolicyVersions: map[string]int64{"returns": 1}})
	if err != nil || order == nil {
		t.Fatalf("the versions in force are accepted: %v", err)
	}
}

func TestPolicySnapshot_WithoutPublishedPolicyTheConfigIsRecorded(t *testing.T) {
	f := newCheckoutFixture()
	f.uc.VersionedPolicies = true
	order := placedOrder(t, f)
	view, err := f.uc.PolicySnapshot(t.Context(), "admin-1", "admin", order.ID)
	if err != nil || view.Order == nil || view.Order.Source != "config" || view.Order.ReturnsWindowDays != 7 || len(view.Order.Policies) != 0 {
		t.Fatalf("the configured rule is recorded explicitly: %v %+v", err, view)
	}

	off := newCheckoutFixture()
	legacy := placedOrder(t, off)
	view, err = off.uc.PolicySnapshot(t.Context(), "buyer-1", "buyer", legacy.ID)
	if err != nil || !view.Legacy {
		t.Fatalf("with the feature off orders keep the legacy rules: %v %+v", err, view)
	}
}

func TestApplyPolicyPublished_IsIdempotentAndRefusesConflicts(t *testing.T) {
	f := newCheckoutFixture()
	at := f.now
	v := domain.PolicyVersion{PolicyID: "33333333-0000-0000-0000-000000000001", Scope: "marketplace", Kind: "terms", Version: 1, ContentHash: "h1", EffectiveAt: at}
	if err := f.uc.ApplyPolicyPublished(t.Context(), v); err != nil {
		t.Fatal(err)
	}
	if err := f.uc.ApplyPolicyPublished(t.Context(), v); err != nil {
		t.Fatalf("a replay is accepted: %v", err)
	}
	v.ContentHash = "other"
	expectCode(t, f.uc.ApplyPolicyPublished(t.Context(), v), apperror.CodeConflict)

	bad := domain.PolicyVersion{PolicyID: "33333333-0000-0000-0000-000000000002", Scope: "marketplace", Kind: "returns", Version: 1, ContentHash: "h",
		EffectiveAt: at, RuleRefs: map[string]string{domain.RuleReturnsWindow: "window-7d", domain.RuleReturnShippingRefund: "seller-pays"}}
	expectCode(t, f.uc.ApplyPolicyPublished(t.Context(), bad), apperror.CodeValidation)
}
