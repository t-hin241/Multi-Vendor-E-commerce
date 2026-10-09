package repository_test

import (
	"testing"

	"github.com/google/uuid"

	"shopee/backend/services/inventory/internal/repository"
)

// AF-03: units of a cancelled vendor order go back once per recovery id,
// with their own movement reason; a return of another id is separate.
func TestRecoveryRestockHappensOncePerRecoveryID(t *testing.T) {
	pool := inventoryDB(t)
	ctx := t.Context()
	items := repository.NewInventoryItemRepository(pool)
	item := stock(t, pool, 5, false)
	recovery := "cancellation:" + uuid.NewString() + ":" + uuid.NewString()

	replayed, err := items.RestockRecovery(ctx, recovery, item.ProductID, nil, 2)
	if err != nil || replayed {
		t.Fatalf("first recovery: %v %v", replayed, err)
	}
	if replayed, err = items.RestockRecovery(ctx, recovery, item.ProductID, nil, 2); err != nil || !replayed {
		t.Fatalf("a repeated recovery is acknowledged without restocking: %v %v", replayed, err)
	}
	if _, err := items.RestockRecovery(ctx, recovery, item.ProductID, nil, 3); err == nil {
		t.Fatal("the same recovery with another quantity must conflict")
	}
	var available int64
	var reason string
	if err := pool.QueryRow(ctx, `SELECT i.available_quantity, m.reason FROM inventory_items i JOIN stock_movements m ON m.inventory_item_id = i.id
		WHERE i.id = $1 AND m.operation_key = $2`, item.ID, "recovery:"+recovery).Scan(&available, &reason); err != nil {
		t.Fatal(err)
	}
	if available != 7 || reason != "cancellation_restock" {
		t.Fatalf("restocked once with its own reason: %d %s", available, reason)
	}

	// AF-04: a failed delivery's sellable units have their own reason.
	delivery := "delivery_exception:" + uuid.NewString() + ":" + uuid.NewString()
	if replayed, err := items.RestockRecovery(ctx, delivery, item.ProductID, nil, 1); err != nil || replayed {
		t.Fatalf("delivery recovery: %v %v", replayed, err)
	}
	if err := pool.QueryRow(ctx, `SELECT m.reason FROM stock_movements m WHERE m.operation_key = $1`, "recovery:"+delivery).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "delivery_return_restock" {
		t.Fatalf("delivery recovery reason: %s", reason)
	}
	assertBalance(t, pool)
}
