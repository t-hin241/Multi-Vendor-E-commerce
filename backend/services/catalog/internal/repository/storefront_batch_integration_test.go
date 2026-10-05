package repository_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"shopee/backend/pkg/vendorsales"
	"shopee/backend/services/catalog/internal/repository"
)

// The storefront read-model refreshes write a whole map in one statement.
func TestStorefrontCacheUpsertsAreBatched(t *testing.T) {
	pool := catalogDB(t)
	ctx := context.Background()
	cache := repository.NewStorefrontCacheRepository(pool)
	a, b := uuid.NewString(), uuid.NewString()

	if err := cache.UpsertVendorNames(ctx, map[string]string{a: "Shop A", b: "Shop B"}); err != nil {
		t.Fatal(err)
	}
	if err := cache.UpsertVendorNames(ctx, map[string]string{a: "Shop A renamed"}); err != nil {
		t.Fatal(err)
	}
	if err := cache.UpsertVendorNames(ctx, map[string]string{}); err != nil {
		t.Fatal("empty refresh must be a no-op:", err)
	}
	names, err := cache.GetVendorNames(ctx, []string{a, b})
	if err != nil || names[a] != "Shop A renamed" || names[b] != "Shop B" {
		t.Fatalf("vendor names = %v, %v", names, err)
	}

	p := uuid.NewString()
	if err := cache.UpsertQuantitySold(ctx, map[string]int64{p: 3}); err != nil {
		t.Fatal(err)
	}
	if err := cache.UpsertQuantitySold(ctx, map[string]int64{p: 7}); err != nil {
		t.Fatal(err)
	}
	sold, err := cache.GetQuantitySold(ctx, []string{p})
	if err != nil || sold[p] != 7 {
		t.Fatalf("quantity sold = %v, %v", sold, err)
	}

	v := uuid.NewString()
	if err := cache.UpsertVariantStock(ctx, map[string]int64{v: 0}); err != nil {
		t.Fatal(err)
	}
	stock, err := cache.GetVariantStock(ctx, []string{v})
	if err != nil || stock[v] != 0 || len(stock) != 1 {
		t.Fatalf("variant stock = %v, %v", stock, err)
	}
}

// Concurrent storefront reads refresh overlapping rows; batched upserts
// must lock them in one order (random map order deadlocked under load).
func TestStorefrontCacheConcurrentRefreshesDoNotDeadlock(t *testing.T) {
	pool := catalogDB(t)
	ctx := context.Background()
	cache := repository.NewStorefrontCacheRepository(pool)
	ids := make([]string, 40)
	for i := range ids {
		ids[i] = uuid.NewString()
	}
	errs := make(chan error, 16*10)
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for round := 0; round < 10; round++ {
				entries := map[string]string{}
				for i := range ids {
					if (i+w+round)%3 != 0 {
						entries[ids[i]] = "shop"
					}
				}
				errs <- cache.UpsertVendorNames(ctx, entries)
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// ApplyAll keeps the per-row version rule of Apply: a newer version wins, an
// older one is ignored, the same version and status refreshes confirmed_at.
func TestVendorSaleStatusApplyAllKeepsTheVersionRule(t *testing.T) {
	pool := catalogDB(t)
	ctx := context.Background()
	store := vendorsales.Store{Pool: pool}
	fresh, older, newer := uuid.NewString(), uuid.NewString(), uuid.NewString()

	if err := store.ApplyAll(ctx, []vendorsales.Status{
		{VendorID: fresh, Status: "approved", Version: 2},
		{VendorID: older, Status: "approved", Version: 5},
		{VendorID: newer, Status: "approved", Version: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE vendor_sale_status SET confirmed_at = now() - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyAll(ctx, []vendorsales.Status{
		{VendorID: fresh, Status: "approved", Version: 2},  // same: refresh only
		{VendorID: older, Status: "suspended", Version: 4}, // older: ignored
		{VendorID: newer, Status: "suspended", Version: 2}, // newer: applied
		{VendorID: newer, Status: "approved", Version: 1},  // duplicate in the page: the newest wins
	}); err != nil {
		t.Fatal(err)
	}

	type row struct {
		status  string
		version int64
		age     time.Duration
	}
	read := func(id string) row {
		var r row
		var seconds float64
		if err := pool.QueryRow(ctx, `SELECT status, version, EXTRACT(EPOCH FROM now() - confirmed_at) FROM vendor_sale_status WHERE vendor_id = $1`, id).Scan(&r.status, &r.version, &seconds); err != nil {
			t.Fatal(err)
		}
		r.age = time.Duration(seconds * float64(time.Second))
		return r
	}
	if r := read(fresh); r.status != "approved" || r.version != 2 || r.age > time.Minute {
		t.Fatalf("same version was not refreshed: %+v", r)
	}
	if r := read(older); r.status != "approved" || r.version != 5 || r.age < 50*time.Minute {
		t.Fatalf("an older version changed the row: %+v", r)
	}
	if r := read(newer); r.status != "suspended" || r.version != 2 {
		t.Fatalf("the newer version was not applied: %+v", r)
	}

	if err := store.ApplyAll(ctx, []vendorsales.Status{{VendorID: "not-a-uuid", Status: "approved", Version: 1}}); err == nil {
		t.Fatal("an invalid status was accepted")
	}
}
