package repository_test

import (
	"context"
	"os"
	"sync"
	"testing"

	"shopee/backend/services/inventory/internal/domain"
	"shopee/backend/services/inventory/internal/repository"
	"shopee/backend/services/inventory/internal/usecase"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type stockVendor struct{ vendor, actor string }

func (v stockVendor) GetApprovedVendorID(_ context.Context, actor, vendor, _ string) (string, error) {
	if actor != v.actor || vendor != v.vendor {
		return "", domain.StockConflict("permission_denied", "Wrong shop")
	}
	return vendor, nil
}

func TestSelfServiceMigrationRoundTripAndHistoryGuard(t *testing.T) {
	pool := inventoryDB(t, "000009")
	ctx := t.Context()
	up, err := os.ReadFile("../../migrations/000010_stock_self_service.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../migrations/000010_stock_self_service.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{string(up), string(down), string(up)} {
		if err = applyMigration(ctx, pool, sql); err != nil {
			t.Fatal(err)
		}
	}
	item := stock(t, pool, 2, false)
	actor := uuid.NewString()
	u := stockUC(pool, item, actor)
	enableStock(t, u, item.VendorID, 10000)
	if _, err = pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("destructive downgrade accepted after policy audit")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM stock_movements WHERE inventory_item_id=$1`, item.ID); err == nil {
		t.Fatal("movement history can be deleted")
	}
}

func TestAppliedApprovalReceiptSurvivesRequesterRevocation(t *testing.T) {
	pool := inventoryDB(t)
	ctx := t.Context()
	item := stock(t, pool, 10, false)
	actor := uuid.NewString()
	u := stockUC(pool, item, actor)
	enableStock(t, u, item.VendorID, 1)
	item, err := u.Items.FindByID(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := u.Adjust(ctx, actor, uuid.NewString(), adjustment(item, 2))
	if err != nil {
		t.Fatal(err)
	}
	admin, key := uuid.NewString(), uuid.NewString()
	decision := usecase.StockDecision{Decision: "approve", ExpectedVersion: item.Version, Reason: "verified received goods"}
	applied, err := u.Decide(ctx, admin, pending.OperationID, key, decision)
	if err != nil {
		t.Fatal(err)
	}
	// The caller still needs admin authorization. The former requester's
	// revoked grant must not turn an already committed result into unknown.
	u.Vendors = stockVendor{item.VendorID, uuid.NewString()}
	replay, err := u.Decide(ctx, admin, pending.OperationID, key, decision)
	if err != nil || replay.OperationID != applied.OperationID || replay.MovementID != applied.MovementID {
		t.Fatalf("receipt %+v %v", replay, err)
	}
	assertBalance(t, pool)
}

func TestSelfServiceCountIncreasePreservesHoldsAndUsesApprovalLimit(t *testing.T) {
	pool := inventoryDB(t)
	ctx := t.Context()
	item := stock(t, pool, 10, false)
	actor := uuid.NewString()
	u := stockUC(pool, item, actor)
	enableStock(t, u, item.VendorID, 5)
	reservations := repository.NewReservationRepository(pool)
	if _, err := reservations.ReserveAtomic(ctx, uuid.NewString(), []domain.ReservationLine{line(item, 3)}); err != nil {
		t.Fatal(err)
	}
	item, err := u.Items.FindByID(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	physical := int64(12)
	a := adjustment(item, 0)
	a.Kind = "count"
	a.PhysicalCount = &physical
	out, err := u.Adjust(ctx, actor, uuid.NewString(), a)
	if err != nil || out.Status != "applied" || out.OnHand != 12 || out.Available != 9 || out.Reserved != 3 {
		t.Fatalf("upward count %+v %v", out, err)
	}
	physical = 20
	a.ExpectedVersion = out.Version
	pending, err := u.Adjust(ctx, actor, uuid.NewString(), a)
	if err != nil || pending.Status != "approval_required" || pending.Available != 9 || pending.Reserved != 3 {
		t.Fatalf("count over limit %+v %v", pending, err)
	}
	assertBalance(t, pool)
}

type stockAdmin struct{}

func (stockAdmin) RequireRole(context.Context, string, string) error { return nil }
func stockUC(pool *pgxpool.Pool, item *domain.InventoryItem, actor string) usecase.StockSelfService {
	return usecase.StockSelfService{Enabled: true, Store: repository.StockSelfService{Pool: pool}, Items: repository.NewInventoryItemRepository(pool), Vendors: stockVendor{item.VendorID, actor}, Ops: usecase.Operations{Transactions: repository.Transactions{Pool: pool}, Identity: stockAdmin{}, Audit: repository.AdminAudit{Pool: pool}}}
}
func enableStock(t *testing.T, u usecase.StockSelfService, vendor string, limit int64) {
	t.Helper()
	if _, err := u.UpdatePolicy(t.Context(), uuid.NewString(), vendor, uuid.NewString(), domain.PolicyChange{Enabled: true, Limits: domain.StockLimits{Receipt: limit}, Reason: "verified pilot warehouse"}); err != nil {
		t.Fatal(err)
	}
}
func adjustment(item *domain.InventoryItem, delta int64) domain.StockAdjustment {
	return domain.StockAdjustment{ItemID: item.ID, Kind: "correction", QuantityDelta: delta, Reason: "physical stock check", EvidenceRef: "fake-evidence-001", ExpectedVersion: item.Version}
}
func TestSelfServiceAtomicReceiptPolicyAndApproval(t *testing.T) {
	pool := inventoryDB(t)
	ctx := t.Context()
	item := stock(t, pool, 20, true)
	actor := uuid.NewString()
	u := stockUC(pool, item, actor)
	item, err := u.Items.FindByID(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	a := adjustment(item, 3)
	key := uuid.NewString()
	if _, err = u.Adjust(ctx, actor, key, a); err == nil {
		t.Fatal("policy defaults must deny")
	}
	enableStock(t, u, item.VendorID, 10)
	out, err := u.Adjust(ctx, actor, key, a)
	if err != nil || out.Available != 23 || out.MovementID == "" {
		t.Fatalf("receipt %+v %v", out, err)
	}
	again, err := u.Adjust(ctx, actor, key, a)
	if err != nil || again != out {
		t.Fatalf("replay %+v %v", again, err)
	}
	a.QuantityDelta = 4
	if _, err = u.Adjust(ctx, actor, key, a); err == nil {
		t.Fatal("changed payload accepted")
	}
	a = adjustment(item, 11)
	a.ExpectedVersion = out.Version
	pending, err := u.Adjust(ctx, actor, uuid.NewString(), a)
	if err != nil || pending.Status != "approval_required" || pending.Available != 23 {
		t.Fatalf("approval %+v %v", pending, err)
	}
	decision := usecase.StockDecision{Decision: "approve", Reason: "verified document", ExpectedVersion: out.Version}
	decisionKey := uuid.NewString()
	admin := uuid.NewString()
	approved, err := u.Decide(ctx, admin, pending.OperationID, decisionKey, decision)
	if err != nil || approved.Available != 34 {
		t.Fatalf("approved %+v %v", approved, err)
	}
	replay, err := u.Decide(ctx, admin, pending.OperationID, decisionKey, decision)
	if err != nil || replay != approved {
		t.Fatalf("decision replay %+v %v", replay, err)
	}
	if _, err = u.Adjust(ctx, uuid.NewString(), uuid.NewString(), a); err == nil {
		t.Fatal("cross vendor accepted")
	}
	a.ExpectedVersion = approved.Version
	pending, err = u.Adjust(ctx, actor, uuid.NewString(), a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = u.UpdatePolicy(ctx, admin, item.VendorID, uuid.NewString(), domain.PolicyChange{Enabled: false, ExpectedVersion: 1, Reason: "pilot revoked"}); err != nil {
		t.Fatal(err)
	}
	decision.ExpectedVersion = approved.Version
	if _, err = u.Decide(ctx, admin, pending.OperationID, uuid.NewString(), decision); err == nil {
		t.Fatal("revoked policy approved")
	}
	if _, err = u.Adjust(ctx, actor, uuid.NewString(), a); err == nil {
		t.Fatal("revoked policy applied")
	}
	original := adjustment(item, 3)
	if replay, err = u.Adjust(ctx, actor, key, original); err != nil || replay != out {
		t.Fatalf("receipt after revoke %+v %v", replay, err)
	}
	assertBalance(t, pool)
}
func TestSelfServiceCountRaceShortageAndAlerts(t *testing.T) {
	pool := inventoryDB(t)
	ctx := t.Context()
	item := stock(t, pool, 20, true)
	actor := uuid.NewString()
	u := stockUC(pool, item, actor)
	enableStock(t, u, item.VendorID, 10000)
	item, err := u.Items.FindByID(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = u.Alerts(ctx, actor, item.ID, uuid.NewString(), domain.AlertSettings{Enabled: true, LowStockThreshold: 15, ExpectedVersion: item.Version}); err != nil {
		t.Fatal(err)
	}
	reservations := repository.NewReservationRepository(pool)
	if _, err = reservations.ReserveAtomic(ctx, uuid.NewString(), []domain.ReservationLine{line(item, 10)}); err != nil {
		t.Fatal(err)
	}
	item, err = u.Items.FindByID(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := int64(5)
	a := adjustment(item, 0)
	a.Kind = "count"
	a.PhysicalCount = &count
	out, err := u.Adjust(ctx, actor, uuid.NewString(), a)
	if err != nil || out.Status != "reserved_stock_conflict" || out.Reserved != 10 {
		t.Fatalf("shortage %+v %v", out, err)
	}
	queue, err := u.Work(ctx, uuid.NewString(), "", 20)
	if err != nil || len(queue.Items) != 1 || *queue.Items[0].PhysicalCount != 5 {
		t.Fatalf("shortage queue %+v %v", queue, err)
	}
	if _, err = u.Adjust(ctx, actor, uuid.NewString(), adjustment(item, 10)); err != nil {
		t.Fatal(err)
	}
	item, err = u.Items.FindByID(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_, _ = reservations.ReserveAtomic(ctx, uuid.NewString(), []domain.ReservationLine{line(item, 1)})
		})
	}
	count = 15
	a = adjustment(item, 0)
	a.Kind = "count"
	a.PhysicalCount = &count
	wg.Go(func() { _, _ = u.Adjust(ctx, actor, uuid.NewString(), a) })
	wg.Wait()
	var episodes int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM stock_alert_episode WHERE item_id=$1`, item.ID).Scan(&episodes); err != nil || episodes != 2 {
		t.Fatalf("episodes %d %v", episodes, err)
	}
	current, err := u.Items.FindByID(ctx, item.ID)
	if err != nil || current.AvailableQuantity < 0 || current.ReservedQuantity < 10 {
		t.Fatalf("stock %+v %v", current, err)
	}
	if _, err = u.Adjust(ctx, actor, uuid.NewString(), a); err == nil {
		t.Fatal("stale count applied")
	}
	assertBalance(t, pool)
}
func TestSelfServiceConcurrentDuplicateAndLegacyRestock(t *testing.T) {
	pool := inventoryDB(t)
	ctx := t.Context()
	item := stock(t, pool, 20, false)
	actor := uuid.NewString()
	u := stockUC(pool, item, actor)
	enableStock(t, u, item.VendorID, 10000)
	item, err := u.Items.FindByID(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	a := adjustment(item, 4)
	key := uuid.NewString()
	var wg sync.WaitGroup
	for range 15 {
		wg.Go(func() {
			if _, err := u.Adjust(ctx, actor, key, a); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	current, err := u.Items.FindByID(ctx, item.ID)
	if err != nil || current.AvailableQuantity != 24 {
		t.Fatalf("duplicate %+v %v", current, err)
	}
	// A pending legacy request remains exclusively in its original approval flow.
	req := &domain.RestockRequest{InventoryItemID: item.ID, ProductID: item.ProductID, VendorID: item.VendorID, RequestedQuantity: 6, RequestedBy: actor}
	legacy := repository.NewRestockRequestRepository(pool)
	if err = legacy.Create(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err = u.Items.Restock(ctx, item.ProductID, 6, uuid.NewString(), req.ID); err != nil {
		t.Fatal(err)
	}
	if err = u.Items.Restock(ctx, item.ProductID, 6, uuid.NewString(), req.ID); err == nil {
		t.Fatal("duplicate legacy movement accepted")
	}
	current, err = u.Items.FindByID(ctx, item.ID)
	if err != nil || current.AvailableQuantity != 30 {
		t.Fatalf("legacy balance %+v %v", current, err)
	}
	assertBalance(t, pool)
}

func TestSelfServiceSecondApprovalAndStaleVersion(t *testing.T) {
	pool := inventoryDB(t)
	ctx := t.Context()
	item := stock(t, pool, 10, false)
	actor := uuid.NewString()
	u := stockUC(pool, item, actor)
	u.Ops.SecondApprovalQuantity = 5
	enableStock(t, u, item.VendorID, 2)
	item, err := u.Items.FindByID(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := u.Adjust(ctx, actor, uuid.NewString(), adjustment(item, 6))
	if err != nil {
		t.Fatal(err)
	}
	admin := uuid.NewString()
	decision := usecase.StockDecision{Decision: "approve", ExpectedVersion: item.Version, Reason: "checked delivery note"}
	first, err := u.Decide(ctx, admin, out.OperationID, uuid.NewString(), decision)
	if err != nil || first.Status != "approval_required" || first.FirstApprovedBy == nil || first.MovementID != "" {
		t.Fatalf("first %+v %v", first, err)
	}
	if _, err = u.Decide(ctx, admin, out.OperationID, uuid.NewString(), decision); err == nil {
		t.Fatal("same admin approved twice")
	}
	final, err := u.Decide(ctx, uuid.NewString(), out.OperationID, uuid.NewString(), decision)
	if err != nil || final.Status != "applied" || final.Available != 16 {
		t.Fatalf("final %+v %v", final, err)
	}
	a := adjustment(item, 6)
	a.ExpectedVersion = final.Version
	out, err = u.Adjust(ctx, actor, uuid.NewString(), a)
	if err != nil {
		t.Fatal(err)
	}
	reservations := repository.NewReservationRepository(pool)
	if _, err = reservations.ReserveAtomic(ctx, uuid.NewString(), []domain.ReservationLine{line(item, 1)}); err != nil {
		t.Fatal(err)
	}
	decision.ExpectedVersion = final.Version
	if _, err = u.Decide(ctx, uuid.NewString(), out.OperationID, uuid.NewString(), decision); err == nil {
		t.Fatal("approved stale count")
	}
	assertBalance(t, pool)
}

func TestSelfServiceAlertDeliveryAndSettingsReplay(t *testing.T) {
	pool := inventoryDB(t)
	ctx := t.Context()
	item := stock(t, pool, 3, true)
	actor := uuid.NewString()
	u := stockUC(pool, item, actor)
	enableStock(t, u, item.VendorID, 10000)
	item, err := u.Items.FindByID(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	settings := domain.AlertSettings{Enabled: true, LowStockThreshold: 5, ExpectedVersion: item.Version}
	out, err := u.Alerts(ctx, actor, item.ID, key, settings)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := u.Alerts(ctx, actor, item.ID, key, settings)
	if err != nil || replay != out {
		t.Fatalf("settings replay %+v %v", replay, err)
	}
	store := repository.StockSelfService{Pool: pool}
	alert, err := store.ClaimAlert(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ParkAlert(ctx, alert); err != nil {
		t.Fatal(err)
	}
	parked, err := u.AlertWork(ctx, uuid.NewString(), "", 20)
	if err != nil || len(parked.Items) != 1 {
		t.Fatalf("parked %+v %v", parked, err)
	}
	if err = u.RetryAlert(ctx, uuid.NewString(), alert.ID, uuid.NewString(), "notification restored"); err != nil {
		t.Fatal(err)
	}
	alert, err = store.ClaimAlert(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.FinishAlert(ctx, alert, true); err != nil {
		t.Fatal(err)
	}
	// Repeated changes below threshold, including toggling alerts, do not
	// produce another episode until available stock recovers.
	settings.ExpectedVersion = out.ExpectedVersion
	settings.Enabled = false
	out, err = u.Alerts(ctx, actor, item.ID, uuid.NewString(), settings)
	if err != nil {
		t.Fatal(err)
	}
	settings.ExpectedVersion = out.ExpectedVersion
	settings.Enabled = true
	out, err = u.Alerts(ctx, actor, item.ID, uuid.NewString(), settings)
	if err != nil {
		t.Fatal(err)
	}
	settings.ExpectedVersion = out.ExpectedVersion
	settings.LowStockThreshold = 1
	out, err = u.Alerts(ctx, actor, item.ID, uuid.NewString(), settings)
	if err != nil {
		t.Fatal(err)
	}
	settings.ExpectedVersion = out.ExpectedVersion
	settings.LowStockThreshold = 5
	if _, err = u.Alerts(ctx, actor, item.ID, uuid.NewString(), settings); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM stock_alert_episode`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("episode spam %d %v", count, err)
	}
	history, err := u.Movements(ctx, actor, item.ID, "", 1)
	if err != nil || len(history.Items) != 1 {
		t.Fatalf("history %+v %v", history, err)
	}
	if _, err = u.Movements(ctx, actor, item.ID, "invalid", 20); err == nil {
		t.Fatal("invalid cursor accepted")
	}
}
