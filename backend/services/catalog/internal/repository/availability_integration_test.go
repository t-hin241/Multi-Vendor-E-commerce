package repository_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"shopee/backend/pkg/vendorsales"
	"shopee/backend/services/catalog/internal/domain"
)

type vacationLookup struct {
	shop   string
	paused bool
}

func (v vacationLookup) Paused(context.Context, []string) (map[string]bool, error) {
	return map[string]bool{v.shop: v.paused}, nil
}

func TestVacationStorefrontAndCheckoutSnapshot(t *testing.T) {
	f := newFixture(t)
	p := f.product(t)
	if err := f.repo.UpdateStatus(t.Context(), p.ID, domain.StatusApproved, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.UpdateActive(t.Context(), p.ID, true); err != nil {
		t.Fatal(err)
	}
	paused, open := false, true
	store := vendorsales.Store{Pool: f.pool}
	if err := store.Apply(t.Context(), vendorsales.Status{VendorID: f.shop, Status: "approved", Version: 2, AcceptingOrders: &paused}); err != nil {
		t.Fatal(err)
	}
	f.uc.Availability = vacationLookup{shop: f.shop, paused: true}
	for _, scope := range []struct {
		category, shop, search string
		want                   int
	}{{"", "", "", 0}, {f.category, "", "Product", 0}, {"", f.shop, "", 1}} {
		rows, err := f.repo.ListStorefront(t.Context(), scope.category, scope.shop, scope.search, 20, 0)
		if err != nil {
			t.Fatal(err)
		}
		count, err := f.repo.CountStorefront(t.Context(), scope.category, scope.shop, scope.search)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != scope.want || count != scope.want {
			t.Fatalf("scope %+v: rows=%d count=%d", scope, len(rows), count)
		}
	}
	snapshot, err := f.uc.ReadCheckout(t.Context(), []string{p.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Products) != 1 || !snapshot.Products[0].ShopPaused || snapshot.Products[0].IsActive {
		t.Fatalf("checkout: %+v", snapshot)
	}
	lookup, err := f.uc.LookupForServices(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !lookup.ShopPaused || lookup.Product.IsActive {
		t.Fatalf("internal lookup: %+v", lookup)
	}
	// Event delivery is versioned; a delayed pre-vacation event cannot reopen listings.
	if err := store.Apply(t.Context(), vendorsales.Status{VendorID: f.shop, Status: "approved", Version: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyAll(t.Context(), []vendorsales.Status{{VendorID: f.shop, Status: "approved", Version: 2, AcceptingOrders: &open}}); err != nil {
		t.Fatal(err)
	}
	count, err := f.repo.CountStorefront(t.Context(), "", "", "")
	if err != nil || count != 0 {
		t.Fatalf("stale event reopened listing: %d %v", count, err)
	}
	started := time.Now()
	if err := store.Apply(t.Context(), vendorsales.Status{VendorID: f.shop, Status: "approved", Version: 3, AcceptingOrders: &open}); err != nil {
		t.Fatal(err)
	}
	count, err = f.repo.CountStorefront(t.Context(), "", "", "")
	if err != nil || count != 1 {
		t.Fatalf("resume: %d %v", count, err)
	}
	t.Logf("local projection apply + visible listing: %s", time.Since(started))
	stored, err := f.repo.FindByID(t.Context(), p.ID)
	if err != nil || !stored.IsActive {
		t.Fatalf("vacation changed product active preference: %+v %v", stored, err)
	}
}

func TestVacationListingManyPausedShops(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	if _, err := f.pool.Exec(ctx, `INSERT INTO vendor_sale_status(vendor_id,status,version,accepting_orders)
		SELECT gen_random_uuid(),'approved',1,n%10=0 FROM generate_series(1,1000) n`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO products(vendor_id,category_id,name,slug,description,price_amount,currency,status,is_active)
		SELECT v.vendor_id,$1,'Vacation load product',v.vendor_id::text||'-'||n,'Synthetic listing fixture',100,'VND','approved',true
		FROM vendor_sale_status v CROSS JOIN generate_series(1,3) n`, f.category); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `ANALYZE vendor_sale_status; ANALYZE products`); err != nil {
		t.Fatal(err)
	}
	durations := make([]time.Duration, 0, 20)
	for range 20 {
		started := time.Now()
		rows, err := f.repo.ListStorefront(ctx, f.category, "", "Vacation", 20, 0)
		if err != nil {
			t.Fatal(err)
		}
		count, err := f.repo.CountStorefront(ctx, f.category, "", "Vacation")
		if err != nil {
			t.Fatal(err)
		}
		durations = append(durations, time.Since(started))
		if count != 300 || len(rows) != 20 {
			t.Fatalf("paused shops leaked: count=%d page=%d", count, len(rows))
		}
	}
	slices.Sort(durations)
	t.Logf("local PostgreSQL: 1000 shops, 900 paused, 3000 products; listing+count n=20 p50=%s p95=%s (not a staging SLO)", durations[10], durations[18])
}
