package usecase_test

import (
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/cart/internal/domain"
	"shopee/backend/services/cart/internal/usecase"
)

func (f *fixture) snapshot(t *testing.T, buyer, op string, expected *int64) *domain.CheckoutOperation {
	t.Helper()
	snap, _, err := f.uc.CreateCheckoutSnapshot(t.Context(), buyer, op, expected)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return snap
}

func purchaseAll(snap *domain.CheckoutOperation) []domain.ConsumeLine {
	lines := make([]domain.ConsumeLine, 0, len(snap.Lines))
	for _, l := range snap.Lines {
		lines = append(lines, domain.ConsumeLine{LineID: l.LineID, Quantity: l.Quantity})
	}
	return lines
}

func quantities(v *usecase.CartView) map[string]int64 {
	out := map[string]int64{}
	for _, l := range v.Lines {
		out[l.ProductID] = l.Quantity
	}
	return out
}

func TestSnapshot_RejectsEmptyCartAndStaleVersion(t *testing.T) {
	f := newFixture()
	f.sellable("p1", 100)
	ctx := t.Context()

	_, _, err := f.uc.CreateCheckoutSnapshot(ctx, "buyer-1", "op-1", nil)
	expectCode(t, err, apperror.CodeValidation)

	if err := f.uc.AddItem(ctx, "buyer-1", "p1", nil, 1, nil); err != nil {
		t.Fatal(err)
	}
	_, _, err = f.uc.CreateCheckoutSnapshot(ctx, "buyer-1", "op-2", ptr(int64(1)))
	expectCode(t, err, domain.CodeCartChanged)
}

func TestSnapshot_ReplayReturnsTheOriginalSnapshot(t *testing.T) {
	f := newFixture()
	f.sellable("p1", 100)
	f.sellable("p2", 100)
	ctx := t.Context()
	if err := f.uc.AddItem(ctx, "buyer-1", "p1", nil, 2, nil); err != nil {
		t.Fatal(err)
	}
	first := f.snapshot(t, "buyer-1", "op-1", nil)

	if err := f.uc.AddItem(ctx, "buyer-1", "p2", nil, 1, nil); err != nil {
		t.Fatal(err)
	}
	again, replayed, err := f.uc.CreateCheckoutSnapshot(ctx, "buyer-1", "op-1", ptr(int64(99)))
	if err != nil || !replayed {
		t.Fatalf("expected a replay, got %v %v", replayed, err)
	}
	if len(again.Lines) != 1 || again.CartVersion != first.CartVersion {
		t.Fatalf("a replay must return the original lines, got %+v", again)
	}

	_, _, err = f.uc.CreateCheckoutSnapshot(ctx, "buyer-2", "op-1", nil)
	expectCode(t, err, apperror.CodeConflict)
}

func TestConsume_KeepsLinesAndUnitsAddedAfterTheSnapshot(t *testing.T) {
	f := newFixture()
	for _, id := range []string{"bought", "raised", "removed-by-buyer", "added-later"} {
		f.sellable(id, 100)
	}
	ctx := t.Context()
	for _, id := range []string{"bought", "raised", "removed-by-buyer"} {
		if err := f.uc.AddItem(ctx, "buyer-1", id, nil, 2, nil); err != nil {
			t.Fatal(err)
		}
	}
	snap := f.snapshot(t, "buyer-1", "op-1", nil)

	// While Order is creating the order, the buyer keeps editing the cart.
	if err := f.uc.AddItem(ctx, "buyer-1", "added-later", nil, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.uc.AddItem(ctx, "buyer-1", "raised", nil, 3, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.uc.RemoveItem(ctx, "buyer-1", "removed-by-buyer", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.uc.AddItem(ctx, "buyer-1", "removed-by-buyer", nil, 1, nil); err != nil {
		t.Fatal(err) // re-added: a new line, not the snapshotted one
	}

	receipt, replayed, err := f.uc.ConsumeCheckout(ctx, "buyer-1", "op-1", purchaseAll(snap))
	if err != nil || replayed {
		t.Fatalf("consume: %v replayed=%v", err, replayed)
	}
	outcomes := map[domain.ConsumeOutcome]int{}
	for _, r := range receipt.Lines {
		outcomes[r.Outcome]++
	}
	if outcomes[domain.ConsumeRemoved] != 1 || outcomes[domain.ConsumeReduced] != 1 || outcomes[domain.ConsumeAlreadyGone] != 1 {
		t.Fatalf("unexpected outcomes %+v", receipt.Lines)
	}

	got := quantities(f.view(t, "buyer-1"))
	want := map[string]int64{"raised": 3, "added-later": 1, "removed-by-buyer": 1}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for id, q := range want {
		if got[id] != q {
			t.Errorf("%s: expected %d, got %d", id, q, got[id])
		}
	}

	// A retried consume is a no-op returning the same receipt.
	again, replayed, err := f.uc.ConsumeCheckout(ctx, "buyer-1", "op-1", purchaseAll(snap))
	if err != nil || !replayed || again.CartVersion != receipt.CartVersion {
		t.Fatalf("expected a replayed receipt, got %+v %v %v", again, replayed, err)
	}
	if after := quantities(f.view(t, "buyer-1")); len(after) != len(want) || after["raised"] != 3 {
		t.Fatalf("a replay must not consume again, got %v", after)
	}
}

func TestConsume_RejectsForeignLinesOtherBuyersAndChangedRequests(t *testing.T) {
	f := newFixture()
	f.sellable("p1", 100)
	f.sellable("p2", 100)
	ctx := t.Context()
	if err := f.uc.AddItem(ctx, "buyer-1", "p1", nil, 2, nil); err != nil {
		t.Fatal(err)
	}
	snap := f.snapshot(t, "buyer-1", "op-1", nil)
	if err := f.uc.AddItem(ctx, "buyer-1", "p2", nil, 1, nil); err != nil {
		t.Fatal(err)
	}
	later := f.view(t, "buyer-1").Lines[1].LineID
	lineID := snap.Lines[0].LineID

	_, _, err := f.uc.ConsumeCheckout(ctx, "buyer-1", "op-1", []domain.ConsumeLine{{LineID: later, Quantity: 1}})
	expectCode(t, err, apperror.CodeValidation)
	_, _, err = f.uc.ConsumeCheckout(ctx, "buyer-1", "op-1", []domain.ConsumeLine{{LineID: lineID, Quantity: 3}})
	expectCode(t, err, apperror.CodeValidation)
	_, _, err = f.uc.ConsumeCheckout(ctx, "buyer-2", "op-1", purchaseAll(snap))
	expectCode(t, err, apperror.CodeConflict)
	_, _, err = f.uc.ConsumeCheckout(ctx, "buyer-1", "missing-op", purchaseAll(snap))
	expectCode(t, err, apperror.CodeNotFound)

	if _, _, err := f.uc.ConsumeCheckout(ctx, "buyer-1", "op-1", []domain.ConsumeLine{{LineID: lineID, Quantity: 1}}); err != nil {
		t.Fatal(err)
	}
	_, _, err = f.uc.ConsumeCheckout(ctx, "buyer-1", "op-1", purchaseAll(snap))
	expectCode(t, err, apperror.CodeConflict)

	if got := quantities(f.view(t, "buyer-1")); got["p1"] != 1 || got["p2"] != 1 {
		t.Fatalf("only the purchased unit may be taken, got %v", got)
	}
}

func TestConsume_ConcurrentWithCartEditsNeverLosesNewLines(t *testing.T) {
	f := newFixture()
	f.sellable("bought", 100)
	for i := range 20 {
		f.sellable("new-"+string(rune('a'+i)), 100)
	}
	ctx := t.Context()
	if err := f.uc.AddItem(ctx, "buyer-1", "bought", nil, 1, nil); err != nil {
		t.Fatal(err)
	}
	snap := f.snapshot(t, "buyer-1", "op-1", nil)

	var wg sync.WaitGroup
	errs := make(chan error, 25)
	for i := range 20 {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			errs <- f.uc.AddItem(ctx, "buyer-1", id, nil, 1, nil)
		}("new-" + string(rune('a'+i)))
	}
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := f.uc.ConsumeCheckout(ctx, "buyer-1", "op-1", purchaseAll(snap))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	got := quantities(f.view(t, "buyer-1"))
	if len(got) != 20 || got["bought"] != 0 {
		t.Fatalf("expected exactly the 20 new lines to remain, got %v", got)
	}
}

func TestRetention_UsesConfiguredWindowsAndBatches(t *testing.T) {
	repo := &fakeRetentionRepository{cartBatches: []int64{2, 2, 1}, opBatches: []int64{1}}
	w := usecase.NewRetentionWorker(repo, usecase.RetentionPolicy{Enabled: true, CartIdle: 180 * 24 * time.Hour,
		OperationTTL: 90 * 24 * time.Hour, Interval: time.Hour, BatchSize: 2}, zerolog.Nop())

	before := time.Now()
	carts, ops, err := w.RunOnce(t.Context())
	if err != nil || carts != 5 || ops != 1 {
		t.Fatalf("unexpected result carts=%d ops=%d err=%v", carts, ops, err)
	}
	if len(repo.cartCalls) != 3 || len(repo.opCalls) != 1 {
		t.Fatalf("expected batches until a short batch, got %d/%d", len(repo.cartCalls), len(repo.opCalls))
	}
	if cutoff := before.Add(-180 * 24 * time.Hour); repo.cartCalls[0].After(cutoff.Add(time.Minute)) || repo.cartCalls[0].Before(cutoff.Add(-time.Minute)) {
		t.Errorf("unexpected idle cutoff %v", repo.cartCalls[0])
	}
}
