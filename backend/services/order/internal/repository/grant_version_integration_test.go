package repository_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/usecase"
)

// PW-021: a seller command records the membership version of the grant
// that allowed it. Vendor revokes the clerk right after the authorize call
// (before Order commits): the return event still shows version 3, the
// version Vendor's membership audit marks as revoked, so the race is
// visible. A command without a shop grant records nothing.
func TestSellerCommandRecordsTheGrantVersionItUsed(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	f := newReturnFixture(t, pool)
	var version atomic.Int64
	version.Store(3)
	vendorSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/vendors/authorize" {
			http.NotFound(w, r)
			return
		}
		v := version.Load()
		version.Store(v + 1) // revoked as soon as the grant was handed out
		fmt.Fprintf(w, `{"data":{"allowed":true,"vendor_id":"%s","status":"approved","role":"staff","membership_version":%d}}`, f.vendor, v)
	}))
	defer vendorSvc.Close()
	f.uc.Vendors = adapter.NewHTTPVendorClient(vendorSvc.URL, serviceauth.Credential("order", "test-internal-service-key-0000000000"))

	rr, err := f.uc.CreateReturn(ctx, f.buyer, usecase.ReturnInput{OrderID: f.order, ItemID: f.item, Quantity: 1, Reason: "Sai size"})
	if err != nil {
		t.Fatal(err)
	}
	request := shopaccess.WithGrants(ctx) // what the router's middleware gives every request
	if _, err := f.uc.VendorConfirmReturn(request, f.vendorUser, rr.ID, "Đồng ý nhận lại"); err != nil {
		t.Fatal(err)
	}
	var used, buyer *int64
	if err := pool.QueryRow(ctx, `SELECT membership_version FROM return_request_events WHERE return_request_id = $1 AND action = 'vendor_confirmed'`, rr.ID).
		Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used == nil || *used != 3 {
		t.Fatalf("the event must record the version that allowed it (3), got %v", used)
	}
	if err := pool.QueryRow(ctx, `SELECT membership_version FROM return_request_events WHERE return_request_id = $1 AND action = 'requested'`, rr.ID).
		Scan(&buyer); err != nil {
		t.Fatal(err)
	}
	if buyer != nil {
		t.Fatalf("a buyer's request has no shop grant, got %d", *buyer)
	}
}
