package repository_test

import (
	"os"
	"testing"

	"github.com/google/uuid"

	"shopee/backend/services/cart/internal/adapter"
	"shopee/backend/services/cart/internal/domain"
	"shopee/backend/services/cart/internal/repository"
	"shopee/backend/services/cart/internal/usecase"
)

func TestSelectiveReceiptSurvivesChangesRollbackAndConsumeRetry(t *testing.T) {
	pool := cartDB(t)
	catalog := &stubCatalog{products: map[string]*adapter.ProductInfo{}}
	uc := newUseCase(pool, catalog)
	uc.SelectiveCheckoutEnabled = true
	buyer := uuid.NewString()
	a, b := catalog.add(1000, "VND"), catalog.add(2000, "VND")
	for _, p := range []string{a, b} {
		if err := uc.AddItem(t.Context(), buyer, p, nil, 2, nil); err != nil {
			t.Fatal(err)
		}
	}
	view, err := uc.View(t.Context(), buyer, usecase.Page{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var selected string
	for _, l := range view.Lines {
		if l.ProductID == a {
			selected = l.LineID
		}
	}
	foreignBuyer := uuid.NewString()
	if err := uc.AddItem(t.Context(), foreignBuyer, a, nil, 1, nil); err != nil {
		t.Fatal(err)
	}
	foreign, err := uc.View(t.Context(), foreignBuyer, usecase.Page{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := uc.CreateCheckoutSnapshot(t.Context(), buyer, uuid.NewString(), &view.Version, []string{foreign.Lines[0].LineID}); err == nil {
		t.Fatal("accepted another buyer's line")
	}
	if _, _, err := uc.CreateCheckoutSnapshot(t.Context(), buyer, uuid.NewString(), &view.Version, []string{selected, selected}); err == nil {
		t.Fatal("accepted duplicate line")
	}
	operation := uuid.NewString()
	snap, _, err := uc.CreateCheckoutSnapshot(t.Context(), buyer, operation, &view.Version, []string{selected})
	if err != nil || len(snap.Lines) != 1 || snap.SelectionHash == "" {
		t.Fatalf("snapshot %+v %v", snap, err)
	}
	down, err := os.ReadFile("../../migrations/000006_selective_checkout.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(down)); err == nil {
		t.Fatal("rollback discarded selected receipts")
	}
	if _, err := pool.Exec(t.Context(), `UPDATE cart_checkout_operations SET lines='[]' WHERE operation_id=$1`, operation); err == nil {
		t.Fatal("mutable snapshot")
	}
	if err := uc.AddItem(t.Context(), buyer, a, nil, 3, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := uc.CreateCheckoutSnapshot(t.Context(), buyer, uuid.NewString(), &view.Version, []string{selected}); err == nil {
		t.Fatal("stale version accepted")
	}
	uc.SelectiveCheckoutEnabled = false
	replay, ok, err := uc.CreateCheckoutSnapshot(t.Context(), buyer, operation, &view.Version, []string{selected})
	if err != nil || !ok || replay.Lines[0].Quantity != 2 {
		t.Fatalf("rollback replay %+v %v", replay, err)
	}
	if _, _, err := uc.ConsumeCheckout(t.Context(), buyer, operation, []domain.ConsumeLine{{LineID: selected, Quantity: 1}}); err == nil {
		t.Fatal("partial receipt accepted")
	}
	lines := []domain.ConsumeLine{{LineID: selected, Quantity: 2}}
	if _, _, err := uc.ConsumeCheckout(t.Context(), buyer, operation, lines); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := uc.ConsumeCheckout(t.Context(), buyer, operation, lines); err != nil || !ok {
		t.Fatalf("consume retry %v", err)
	}
	quantities, _ := viewQuantities(t, uc, buyer)
	if quantities[a] != 3 || quantities[b] != 2 {
		t.Fatalf("lost unpurchased quantities: %+v", quantities)
	}
	if _, _, err := uc.CreateCheckoutSnapshot(t.Context(), uuid.NewString(), operation, &view.Version, []string{selected}); err == nil {
		t.Fatal("cross-buyer replay")
	}
}

func TestSelectiveMigrationPreservesLegacySnapshot(t *testing.T) {
	pool := cartDB(t)
	catalog := &stubCatalog{products: map[string]*adapter.ProductInfo{}}
	uc := newUseCase(pool, catalog)
	buyer, op := uuid.NewString(), uuid.NewString()
	product := catalog.add(1000, "VND")
	if err := uc.AddItem(t.Context(), buyer, product, nil, 2, nil); err != nil {
		t.Fatal(err)
	}
	original, _, err := uc.CreateCheckoutSnapshot(t.Context(), buyer, op, nil)
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../migrations/000006_selective_checkout.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(down)); err != nil {
		t.Fatal(err)
	}
	if err := uc.AddItem(t.Context(), buyer, product, nil, 3, nil); err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../../migrations/000006_selective_checkout.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(up)); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.NewCheckoutOperationRepository(pool).Find(t.Context(), op)
	if err != nil || stored.SelectionHash != "" || len(stored.Lines) != 1 || stored.Lines[0].Quantity != 2 || stored.Lines[0].LineID != original.Lines[0].LineID {
		t.Fatalf("legacy receipt changed: %+v %v", stored, err)
	}
}

func TestUnselectedChangesAndDisjointCheckoutRequireFreshVersion(t *testing.T) {
	pool := cartDB(t)
	catalog := &stubCatalog{products: map[string]*adapter.ProductInfo{}}
	uc := newUseCase(pool, catalog)
	uc.SelectiveCheckoutEnabled = true
	buyer := uuid.NewString()
	a, b := catalog.add(1000, "VND"), catalog.add(2000, "VND")
	for _, p := range []string{a, b} {
		if err := uc.AddItem(t.Context(), buyer, p, nil, 1, nil); err != nil {
			t.Fatal(err)
		}
	}
	v, err := uc.View(t.Context(), buyer, usecase.Page{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	first, second := v.Lines[0], v.Lines[1]
	if err := uc.AddItem(t.Context(), buyer, second.ProductID, nil, 1, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := uc.CreateCheckoutSnapshot(t.Context(), buyer, uuid.NewString(), &v.Version, []string{first.LineID}); err == nil {
		t.Fatal("unselected change did not invalidate version")
	}
	v, err = uc.View(t.Context(), buyer, usecase.Page{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	op := uuid.NewString()
	snap, _, err := uc.CreateCheckoutSnapshot(t.Context(), buyer, op, &v.Version, []string{first.LineID})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := uc.ConsumeCheckout(t.Context(), buyer, op, []domain.ConsumeLine{{LineID: first.LineID, Quantity: snap.Lines[0].Quantity}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := uc.CreateCheckoutSnapshot(t.Context(), buyer, uuid.NewString(), &v.Version, []string{second.LineID}); err == nil {
		t.Fatal("disjoint checkout accepted old cart version")
	}
}
