package repository_test

import (
	"context"
	"os"
	"path/filepath"
	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
	"shopee/backend/services/order/internal/usecase"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

func orderDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("ORDER_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("ORDER_TEST_DATABASE_URL is not configured")
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
	schema := "order_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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

type inventoryReceiptStub struct {
	mu        sync.Mutex
	committed bool
	expired   bool
}

func (s *inventoryReceiptStub) Reserve(context.Context, string, []adapter.ReserveLine) error {
	return nil
}
func (s *inventoryReceiptStub) Release(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.committed {
		return apperror.Conflict("Already committed")
	}
	return nil
}
func (s *inventoryReceiptStub) Commit(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expired {
		return apperror.Conflict("Hold expired")
	}
	s.committed = true
	return nil
}
func (s *inventoryReceiptStub) Operation(_ context.Context, id string) (*adapter.ReservationReceipt, error) {
	status := "held"
	if s.expired {
		status = "expired"
	}
	return &adapter.ReservationReceipt{OrderID: id, OperationID: id, Status: status}, nil
}

type notifyStub struct{}

func (notifyStub) Notify(context.Context, string, string, string) error { return nil }

type shipmentStub struct{}

func (shipmentStub) CreateShipment(context.Context, adapter.CreateShipmentInput) (string, int64, error) {
	return "", 0, nil
}
func (shipmentStub) CancelForVendorOrder(context.Context, string) error { return nil }
func TestOrderInventoryReceiptPrecedesAtomicPaidState(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	order, vo := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO orders(id,buyer_id,total_amount,currency,recipient_name,phone,province,district,ward,street_address) VALUES($1,$2,100,'VND','Test Recipient','0000000000','Test','Test','Test','Test street')`, order, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO vendor_orders(id,order_id,vendor_id,subtotal_amount,currency) VALUES($1,$2,$3,100,'VND')`, vo, order, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	orders := repository.NewOrderRepository(pool)
	vendorOrders := repository.NewVendorOrderRepository(pool)
	stock := &inventoryReceiptStub{}
	uc := usecase.NewOrderUseCase(orders, vendorOrders, nil, repository.NewCommissionRuleRepository(pool), repository.NewCartConsumptionRepository(pool), nil, nil, nil, stock, shipmentStub{}, notifyStub{}, zerolog.Nop())
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_paid() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test vendor write failure'; END $$; CREATE TRIGGER reject_paid BEFORE UPDATE ON vendor_orders FOR EACH ROW EXECUTE FUNCTION reject_paid()`); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.MarkPaid(ctx, order); err == nil {
		t.Fatal("expected paid transaction failure")
	}
	persisted, err := orders.FindByID(ctx, order)
	if err != nil || persisted.Status != domain.StatusPendingPayment {
		t.Fatal("parent paid survived vendor write rollback")
	}
	if !stock.committed {
		t.Fatal("inventory receipt not requested first")
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_paid ON vendor_orders`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := uc.MarkPaid(ctx, order); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	persisted, err = orders.FindByID(ctx, order)
	if err != nil || persisted.Status != domain.StatusPaid {
		t.Fatal("retry did not converge")
	}
	child, err := vendorOrders.FindByID(ctx, vo)
	if err != nil || child.Status != domain.StatusPaid {
		t.Fatal("vendor order not paid")
	}
	if _, err := uc.MarkPaymentFailed(ctx, order, "late failure"); err == nil {
		t.Fatal("late failed outcome cancelled committed stock")
	}
}
func TestOrderLatePaymentAndExpiryEvent(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO orders(id,buyer_id,total_amount,currency,recipient_name,phone,province,district,ward,street_address) VALUES($1,$2,100,'VND','Test Recipient','0000000000','Test','Test','Test','Test street')`, id, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	orders := repository.NewOrderRepository(pool)
	stock := &inventoryReceiptStub{expired: true}
	uc := usecase.NewOrderUseCase(orders, repository.NewVendorOrderRepository(pool), nil, repository.NewCommissionRuleRepository(pool), repository.NewCartConsumptionRepository(pool), nil, nil, nil, stock, shipmentStub{}, notifyStub{}, zerolog.Nop())
	if _, err := uc.MarkPaid(ctx, id); err == nil {
		t.Fatal("late payment fulfilled expired stock")
	}
	for i := 0; i < 2; i++ {
		if err := uc.ReservationExpired(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	persisted, err := orders.FindByID(ctx, id)
	if err != nil || persisted.Status != domain.StatusCancelled {
		t.Fatal("expiry event did not cancel order")
	}
	if _, err := uc.MarkPaid(ctx, id); err == nil {
		t.Fatal("cancelled order resurrected")
	}
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
