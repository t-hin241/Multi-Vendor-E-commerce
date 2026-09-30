package repository_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"shopee/backend/pkg/apperror"
	"shopee/backend/services/inventory/internal/domain"
	"shopee/backend/services/inventory/internal/repository"
	"shopee/backend/services/inventory/internal/usecase"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func inventoryDB(t *testing.T, maxVersion ...string) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("INVENTORY_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("INVENTORY_TEST_DATABASE_URL is not configured")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("database name must end in _test")
	}
	ctx := t.Context()
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("open test database")
	}
	schema := "inventory_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg = cfg.Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("open isolated schema")
	}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	migrations, err := filepath.Glob("../../migrations/*.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range migrations {
		if len(maxVersion) > 0 && filepath.Base(file)[:6] > maxVersion[0] {
			continue
		}
		sql, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyMigration(ctx, pool, string(sql)); err != nil {
			t.Fatalf("migration %s: %v", filepath.Base(file), err)
		}
	}
	return pool
}

// applyMigration retries the one race that test schemas of other packages
// sharing this database can cause: concurrent CREATE EXTENSION IF NOT
// EXISTS collide on pg_extension's unique index until one commits.
func applyMigration(ctx context.Context, pool *pgxpool.Pool, sql string) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if _, err = pool.Exec(ctx, sql); err == nil || !strings.Contains(err.Error(), "pg_extension_name_index") {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
	}
	return err
}

func stock(t *testing.T, pool *pgxpool.Pool, quantity int64, variant bool) *domain.InventoryItem {
	t.Helper()
	item := &domain.InventoryItem{ProductID: uuid.NewString(), VendorID: uuid.NewString(), ActorUserID: uuid.NewString(), AvailableQuantity: quantity}
	if variant {
		id := uuid.NewString()
		item.VariantID = &id
	}
	if err := repository.NewInventoryItemRepository(pool).Create(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	return item
}
func line(i *domain.InventoryItem, q int64) domain.ReservationLine {
	return domain.ReservationLine{ProductID: i.ProductID, VariantID: i.VariantID, Quantity: q}
}
func assertBalance(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var mismatches int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM inventory_items i LEFT JOIN (SELECT inventory_item_id,sum(quantity) qty FROM stock_reservations WHERE status='active' GROUP BY inventory_item_id) s ON s.inventory_item_id=i.id WHERE i.available_quantity<0 OR i.reserved_quantity<0 OR i.reserved_quantity<>coalesce(s.qty,0)`).Scan(&mismatches); err != nil {
		t.Fatal(err)
	}
	if mismatches != 0 {
		t.Fatal("reservation balance drift")
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM inventory_items i JOIN (SELECT inventory_item_id,sum(change_quantity) available,sum(reserved_change) reserved FROM stock_movements GROUP BY inventory_item_id)m ON m.inventory_item_id=i.id WHERE i.available_quantity<>m.available OR i.reserved_quantity<>m.reserved`).Scan(&mismatches); err != nil {
		t.Fatal(err)
	}
	if mismatches != 0 {
		t.Fatal("movement ledger drift")
	}
}
func TestInventoryConcurrentReserveAndIdempotency(t *testing.T) {
	pool := inventoryDB(t)
	item := stock(t, pool, 20, true)
	repo := repository.NewReservationRepository(pool)
	ctx := t.Context()
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := repo.ReserveAtomic(ctx, uuid.NewString(), []domain.ReservationLine{line(item, 1)}); err == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 20 {
		t.Fatalf("stock winners=%d", successes.Load())
	}
	assertBalance(t, pool)
	item = stock(t, pool, 50, false)
	id := uuid.NewString()
	successes.Store(0)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := repo.ReserveAtomic(ctx, id, []domain.ReservationLine{line(item, 2), line(item, 3)}); err == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 20 {
		t.Fatalf("idempotent retries failed: %d", successes.Load())
	}
	op, err := repo.Operation(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(op.Items) != 1 || op.Items[0].Quantity != 5 {
		t.Fatal("duplicates not merged")
	}
	if _, err := repo.ReserveAtomic(ctx, id, []domain.ReservationLine{line(item, 4)}); err == nil {
		t.Fatal("changed payload accepted")
	}
	if err := repo.CommitByOrderID(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitByOrderID(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReleaseByOrderID(ctx, id); err == nil {
		t.Fatal("committed stock released")
	}
	assertBalance(t, pool)
}
func TestInventoryAtomicLinesMappingAndCancellationTombstone(t *testing.T) {
	pool := inventoryDB(t)
	first := stock(t, pool, 8, false)
	second := stock(t, pool, 0, true)
	repo := repository.NewReservationRepository(pool)
	id := uuid.NewString()
	ctx := t.Context()
	if _, err := repo.ReserveAtomic(ctx, id, []domain.ReservationLine{line(first, 2), line(second, 1)}); err == nil {
		t.Fatal("insufficient batch accepted")
	}
	var total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reservation_operations`).Scan(&total); err != nil || total != 0 {
		t.Fatal("partial operation persisted")
	}
	assertBalance(t, pool)
	invalid := line(second, 1)
	invalid.ProductID = first.ProductID
	if _, err := repo.ReserveAtomic(ctx, id, []domain.ReservationLine{invalid}); err == nil {
		t.Fatal("wrong product-variant mapping accepted")
	}
	if err := repo.CommitByOrderID(ctx, id); err == nil {
		t.Fatal("unreserved commit succeeded")
	}
	if err := repo.ReleaseByOrderID(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReserveAtomic(ctx, id, []domain.ReservationLine{line(first, 1)}); err == nil {
		t.Fatal("reserve resurrected cancelled operation")
	}
	assertBalance(t, pool)
}
func TestInventoryTerminalRacesAndExpiryRestart(t *testing.T) {
	pool := inventoryDB(t)
	item := stock(t, pool, 40, true)
	ctx := t.Context()
	repo := repository.NewReservationRepository(pool)
	for i := 0; i < 12; i++ {
		id := uuid.NewString()
		if _, err := repo.ReserveAtomic(ctx, id, []domain.ReservationLine{line(item, 1)}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _ = repo.CommitByOrderID(ctx, id) }()
		go func() { defer wg.Done(); _ = repo.ReleaseByOrderID(ctx, id) }()
		wg.Wait()
		op, err := repo.Operation(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if op.Status != "committed" && op.Status != "released" {
			t.Fatal("terminal race did not converge")
		}
	}
	id := uuid.NewString()
	if _, err := repo.ReserveAtomic(ctx, id, []domain.ReservationLine{line(item, 2)}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE reservation_operations SET expires_at=now()-interval '1 second' WHERE order_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitByOrderID(ctx, id); err == nil {
		t.Fatal("late capture committed")
	}
	if err := repository.NewReservationRepository(pool).ExpireOne(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.ExpireOne(ctx); !errors.Is(err, repository.ErrNoDueReservation) {
		t.Fatalf("unexpected second expiry: %v", err)
	}
	if err := repo.ReleaseByOrderID(ctx, id); err != nil {
		t.Fatal(err)
	}
	op, err := repo.Operation(ctx, id)
	if err != nil || op.Status != "expired" {
		t.Fatal("expiry receipt lost")
	}
	assertBalance(t, pool)
	var events int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inventory_outbox WHERE order_id=$1`, id).Scan(&events); err != nil || events != 1 {
		t.Fatal("expiry event not atomic")
	}
}
func TestInventoryInitialStockAndRestockRollback(t *testing.T) {
	pool := inventoryDB(t)
	ctx := t.Context()
	items := repository.NewInventoryItemRepository(pool)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_movement() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test movement failure'; END $$; CREATE TRIGGER reject_movement BEFORE INSERT ON stock_movements FOR EACH ROW EXECUTE FUNCTION reject_movement()`); err != nil {
		t.Fatal(err)
	}
	item := &domain.InventoryItem{ProductID: uuid.NewString(), VendorID: uuid.NewString(), ActorUserID: uuid.NewString(), AvailableQuantity: 10}
	if err := items.Create(ctx, item); err == nil {
		t.Fatal("expected audit failure")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inventory_items`).Scan(&count); err != nil || count != 0 {
		t.Fatal("initial stock survived audit rollback")
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_movement ON stock_movements`); err != nil {
		t.Fatal(err)
	}
	item = stock(t, pool, 5, false)
	requests := repository.NewRestockRequestRepository(pool)
	req := &domain.RestockRequest{InventoryItemID: item.ID, ProductID: item.ProductID, VendorID: item.VendorID, RequestedQuantity: 7, RequestedBy: uuid.NewString()}
	if err := requests.Create(ctx, req); err != nil {
		t.Fatal(err)
	}
	admin := uuid.NewString()
	uc := usecase.NewInventoryUseCase(items, repository.NewReservationRepository(pool), requests, nil, nil, usecase.Operations{Transactions: repository.Transactions{Pool: pool}, Identity: testIdentity{admin}})
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_decision() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test decision failure'; END $$; CREATE TRIGGER reject_decision BEFORE UPDATE ON restock_requests FOR EACH ROW EXECUTE FUNCTION reject_decision()`); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.ApproveRestockRequest(ctx, admin, req.ID); err == nil {
		t.Fatal("expected decision failure")
	}
	current, err := items.FindByProductID(ctx, item.ProductID)
	if err != nil || current.AvailableQuantity != 5 {
		t.Fatal("restock survived decision rollback")
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_decision ON restock_requests`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := uc.ApproveRestockRequest(ctx, admin, req.ID); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	current, err = items.FindByProductID(ctx, item.ProductID)
	if err != nil || current.AvailableQuantity != 12 {
		t.Fatal("approval retry changed stock twice")
	}
	if _, err := uc.ApproveRestockRequest(ctx, uuid.NewString(), req.ID); err == nil {
		t.Fatal("non-admin approval accepted")
	}
	assertBalance(t, pool)
}

type testIdentity struct{ admin string }

func (v testIdentity) RequireRole(_ context.Context, user, role string) error {
	if user != v.admin || role != "admin" {
		return apperror.Forbidden("Not admin")
	}
	return nil
}
func TestInventoryLegacyAndOutboxReplay(t *testing.T) {
	pool := inventoryDB(t)
	ctx := t.Context()
	repo := repository.NewReservationRepository(pool)
	item := stock(t, pool, 4, false)
	id := uuid.NewString()
	if _, err := repo.ReserveAtomic(ctx, id, []domain.ReservationLine{line(item, 1)}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE reservation_operations SET expires_at=now()-interval '1 hour',legacy=true WHERE order_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := repo.ExpireOne(ctx); !errors.Is(err, repository.ErrNoDueReservation) {
		t.Fatal("legacy hold auto expired")
	}
	if _, err := pool.Exec(ctx, `UPDATE reservation_operations SET legacy=false WHERE order_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := repo.ExpireOne(ctx); err != nil {
		t.Fatal(err)
	}
	ops := repository.Maintenance{Pool: pool}
	fail := func(context.Context, repository.OutboxEvent) error { return errors.New("test unavailable") }
	for i := 0; i < 10; i++ {
		if _, err := pool.Exec(ctx, `UPDATE inventory_outbox SET next_attempt_at=now()`); err != nil {
			t.Fatal(err)
		}
		if err := ops.Dispatch(ctx, fail); err != nil {
			t.Fatal(err)
		}
	}
	if err := ops.Dispatch(ctx, fail); !errors.Is(err, repository.ErrNoInventoryEvent) {
		t.Fatal("parked event retried")
	}
	if err := ops.Repair(ctx, id, uuid.NewString(), "replay", "Test recovery"); err != nil {
		t.Fatal(err)
	}
	if err := ops.Dispatch(ctx, func(context.Context, repository.OutboxEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	stats, err := ops.Stats(ctx)
	if err != nil || stats["events_pending"] != 0 {
		t.Fatalf("event still pending: %v", err)
	}
	assertBalance(t, pool)
}

func TestInventoryLegacyMigrationPreservesHolds(t *testing.T) {
	pool := inventoryDB(t, "000004")
	ctx := t.Context()
	id, item, product := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO inventory_items(id,product_id,vendor_id,available_quantity,reserved_quantity) VALUES($1,$2,$3,8,2)`, item, product, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO stock_reservations(inventory_item_id,order_id,quantity,expires_at) VALUES($1,$2,2,now()-interval '1 day')`, item, id); err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile("../../migrations/000005_reservation_operations.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	repo := repository.NewReservationRepository(pool)
	o, err := repo.Operation(ctx, id)
	if err != nil || !o.Legacy || o.Status != "held" {
		t.Fatal("legacy hold changed during backfill")
	}
	if err := repo.ExpireOne(ctx); !errors.Is(err, repository.ErrNoDueReservation) {
		t.Fatal("legacy hold auto-released")
	}
	if _, err := repo.ReserveAtomic(ctx, id, []domain.ReservationLine{{ProductID: product, Quantity: 2}}); err != nil {
		t.Fatal("legacy retry not recognized", err)
	}
	var reserved int64
	if err := pool.QueryRow(ctx, `SELECT reserved_quantity FROM inventory_items WHERE id=$1`, item).Scan(&reserved); err != nil || reserved != 2 {
		t.Fatal("legacy stock drift")
	}
}
func TestInventoryExpiryFailureAndConcurrentCancellation(t *testing.T) {
	pool := inventoryDB(t)
	ctx := t.Context()
	item := stock(t, pool, 10, false)
	repo := repository.NewReservationRepository(pool)
	id := uuid.NewString()
	if _, err := repo.ReserveAtomic(ctx, id, []domain.ReservationLine{line(item, 2)}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE reservation_operations SET expires_at=now()-interval '1 second';CREATE FUNCTION reject_expiry() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test movement failure'; END $$; CREATE TRIGGER reject_expiry BEFORE INSERT ON stock_movements FOR EACH ROW EXECUTE FUNCTION reject_expiry()`); err != nil {
		t.Fatal(err)
	}
	if err := repo.ExpireOne(ctx); err == nil {
		t.Fatal("expected expiry rollback")
	}
	assertBalance(t, pool)
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT expiry_attempts FROM reservation_operations WHERE order_id=$1`, id).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatal("expiry retry lost")
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_expiry ON stock_movements;UPDATE reservation_operations SET expiry_next_at=now()`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); _ = repo.ExpireOne(ctx) }()
	go func() { defer wg.Done(); _ = repo.ReleaseByOrderID(ctx, id) }()
	go func() {
		defer wg.Done()
		if err := repo.CommitByOrderID(ctx, id); err == nil {
			t.Error("late commit won")
		}
	}()
	wg.Wait()
	o, err := repo.Operation(ctx, id)
	if err != nil || o.Status != "released" && o.Status != "expired" {
		t.Fatal("terminal race not resolved")
	}
	assertBalance(t, pool)
	var movements int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM stock_movements WHERE reference_id=$1 AND reason IN ('reservation_released','reservation_expired')`, id).Scan(&movements); err != nil || movements != 1 {
		t.Fatal("terminal movement duplicated")
	}
}
func TestInventoryRestockOverflowAndCacheDelivery(t *testing.T) {
	pool := inventoryDB(t)
	ctx := t.Context()
	item := stock(t, pool, 9223372036854775807, true)
	items := repository.NewInventoryItemRepository(pool)
	if err := items.RestockVariant(ctx, *item.VariantID, 1, uuid.NewString(), uuid.NewString()); err == nil {
		t.Fatal("restock overflow accepted")
	}
	assertBalance(t, pool)
	ops := repository.Maintenance{Pool: pool}
	if err := ops.SyncStock(ctx, func(context.Context, []string) error { return errors.New("test cache unavailable") }); err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT attempts FROM inventory_stock_outbox WHERE variant_id=$1`, item.VariantID).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatal("cache retry missing")
	}
	if _, err := pool.Exec(ctx, `UPDATE inventory_stock_outbox SET next_attempt_at=now()`); err != nil {
		t.Fatal(err)
	}
	if err := ops.SyncStock(ctx, func(_ context.Context, ids []string) error {
		if len(ids) != 1 || ids[0] != *item.VariantID {
			t.Fatal("incorrect cache scope")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stats, err := ops.Stats(ctx)
	if err != nil || stats["cache_pending"] != 0 {
		t.Fatal("cache ACK lost")
	}
}
