package repository_test

import (
	"testing"

	"github.com/google/uuid"

	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/usecase"
)

// AF-03: Order stops a vendor order it agreed to cancel. Before handover
// the shipment is cancelled (or there is none); after handover nothing
// changes here and the answer says so. A refused handover claim keeps
// the package from shipping.
func TestStopFulfillmentBeforeAndAfterHandover(t *testing.T) {
	e := newEnv(t, false)
	ctx := t.Context()
	vendor := usecase.Actor{ID: e.userA, Role: domain.ActorVendor}

	if result, err := e.uc.StopFulfillment(ctx, uuid.NewString(), "fake-op-1"); err != nil || result != usecase.StopStopped {
		t.Fatalf("no shipment yet: %s %v", result, err)
	}
	pending := e.paidShipment(t)
	for i := 0; i < 2; i++ {
		if result, err := e.uc.StopFulfillment(ctx, pending.VendorOrderID, "fake-op-2"); err != nil || result != usecase.StopStopped {
			t.Fatalf("stop before handover: %s %v", result, err)
		}
	}
	if e.count(t, `SELECT count(*) FROM shipments WHERE id = $1 AND status = 'cancelled'`, pending.ID) != 1 {
		t.Fatal("a stopped package is cancelled")
	}
	if _, err := e.uc.MarkShipped(ctx, vendor, pending.ID, "TRACK-STOP-1"); err == nil {
		t.Fatal("a stopped package cannot ship")
	}

	shipped := e.paidShipment(t)
	if _, err := e.uc.MarkShipped(ctx, vendor, shipped.ID, "TRACK-STOP-2"); err != nil {
		t.Fatal(err)
	}
	if result, err := e.uc.StopFulfillment(ctx, shipped.VendorOrderID, "fake-op-3"); err != nil || result != usecase.StopHandedOver {
		t.Fatalf("after handover: %s %v", result, err)
	}
	if e.count(t, `SELECT count(*) FROM shipments WHERE id = $1 AND status = 'shipped'`, shipped.ID) != 1 {
		t.Fatal("a stop never changes a package already with the carrier")
	}

	// Order refuses the grant (a cancellation is open): no handover.
	fenced := e.paidShipment(t)
	e.orders.mu.Lock()
	e.orders.vos[fenced.VendorOrderID].Fulfillable = false
	e.orders.mu.Unlock()
	if _, err := e.uc.MarkShipped(ctx, vendor, fenced.ID, "TRACK-STOP-3"); err == nil {
		t.Fatal("a refused handover claim must keep the package")
	}
	if e.count(t, `SELECT count(*) FROM shipments WHERE id = $1 AND status IN ('pending', 'ready_to_ship')`, fenced.ID) != 1 {
		t.Fatal("the fenced package stays unshipped")
	}
}
