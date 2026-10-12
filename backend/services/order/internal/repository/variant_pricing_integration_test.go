package repository_test

import (
	"github.com/google/uuid"
	"shopee/backend/services/order/internal/repository"
	"testing"
)

func TestSKUPriceSnapshotPersistsAndCannotBeRewritten(t *testing.T) {
	pool := orderDB(t)
	plan := seedPlan(t, pool, uuid.NewString())
	variant := uuid.NewString()
	plan.Items[0].VariantID = &variant
	plan.Items[0].PricingSource = "variant"
	plan.Items[0].CatalogProductVersion = 1
	plan.Items[0].CatalogVariantVersion = 7
	repo := repository.NewOrderRepository(pool)
	order, err := repo.CreateFromPlan(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	items, err := repo.ListItemsByOrder(t.Context(), order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].PriceAmount != 1000 || items[0].CatalogVariantVersion != 7 || items[0].PricingSource != "variant" {
		t.Fatalf("lost snapshot: %+v", items)
	}
	if _, err = pool.Exec(t.Context(), `UPDATE order_items SET price_amount=900 WHERE id=$1`, items[0].ID); err == nil {
		t.Fatal("historical price changed")
	}
	if _, err = pool.Exec(t.Context(), `UPDATE order_items SET catalog_variant_version=8 WHERE id=$1`, items[0].ID); err == nil {
		t.Fatal("historical version changed")
	}
}
