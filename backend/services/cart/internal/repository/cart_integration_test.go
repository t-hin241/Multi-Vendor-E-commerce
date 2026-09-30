package repository_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/cart/internal/adapter"
	"shopee/backend/services/cart/internal/domain"
	"shopee/backend/services/cart/internal/repository"
	"shopee/backend/services/cart/internal/usecase"
)

// cartDB opens an isolated schema in the database named by
// CART_TEST_DATABASE_URL (which must end in _test) and applies every
// migration to it.
func cartDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("CART_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("CART_TEST_DATABASE_URL is not configured")
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
	schema := "cart_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg = cfg.Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.MaxConns = 20
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
	files, err := filepath.Glob("../../migrations/*.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, file := range files {
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

// applyMigration retries the one race other packages' test schemas can
// cause on a shared test database: two sessions running CREATE EXTENSION
// IF NOT EXISTS at the same moment collide on pg_extension's unique index.
// Once one of them commits, the retry sees the extension and skips it.
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

type stubCatalog struct {
	mu       sync.Mutex
	products map[string]*adapter.ProductInfo
}

func (s *stubCatalog) add(price int64, currency string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := uuid.NewString()
	s.products[id] = &adapter.ProductInfo{ID: id, Name: "P", PriceAmount: price, Currency: currency, Status: "approved", IsVisible: true}
	return id
}

func (s *stubCatalog) GetProduct(_ context.Context, id string) (*adapter.ProductInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.products[id]
	if !ok {
		return nil, apperror.NotFound("Product not found")
	}
	copyProduct := *p
	return &copyProduct, nil
}

func (s *stubCatalog) GetVariant(context.Context, string) (*adapter.VariantInfo, error) {
	return nil, apperror.NotFound("Product option not found")
}

type stubInventory struct{}

func (stubInventory) GetVariantStock(context.Context, []string) (map[string]int64, error) {
	return map[string]int64{}, nil
}
func (stubInventory) GetProductStock(context.Context, string) (int64, bool, error) {
	return 100, true, nil
}

func newUseCase(pool *pgxpool.Pool, catalog *stubCatalog) *usecase.CartUseCase {
	return usecase.NewCartUseCase(repository.Transactions{Pool: pool}, repository.NewCartRepository(pool),
		repository.NewCartItemRepository(pool), repository.NewCheckoutOperationRepository(pool), catalog, stubInventory{}, zerolog.Nop())
}

func viewQuantities(t *testing.T, uc *usecase.CartUseCase, buyer string) (map[string]int64, int64) {
	t.Helper()
	v, err := uc.View(t.Context(), buyer, usecase.Page{Limit: usecase.MaxPageLimit})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, l := range v.Lines {
		out[l.ProductID] = l.Quantity
	}
	return out, v.Version
}

func TestCartSchemaEnforcesLimitsAndLineUniqueness(t *testing.T) {
	pool := cartDB(t)
	ctx := t.Context()
	var cartID string
	if err := pool.QueryRow(ctx, `INSERT INTO carts (user_id) VALUES ($1) RETURNING id`, uuid.NewString()).Scan(&cartID); err != nil {
		t.Fatal(err)
	}
	product := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO cart_items (cart_id, product_id, quantity) VALUES ($1, $2, 1000)`, cartID, product); err == nil {
		t.Error("quantity above the per-line limit must be rejected by the database")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO cart_items (cart_id, product_id, quantity) VALUES ($1, $2, 1)`, cartID, product); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO cart_items (cart_id, product_id, quantity) VALUES ($1, $2, 1)`, cartID, product); err == nil {
		t.Error("two product-level lines (NULL variant) for one product must collide")
	}
	variant := uuid.NewString()
	for i, want := range []bool{true, false} {
		_, err := pool.Exec(ctx, `INSERT INTO cart_items (cart_id, product_id, variant_id, quantity) VALUES ($1, $2, $3, 1)`, cartID, product, variant)
		if (err == nil) != want {
			t.Errorf("variant insert %d: expected success=%v, got %v", i, want, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO cart_items (cart_id, product_id, quantity, seen_price_amount) VALUES ($1, $2, 1, 10)`, cartID, uuid.NewString()); err == nil {
		t.Error("a price reference without a currency must be rejected")
	}
}

func TestConcurrentAddsRespectTheLineLimit(t *testing.T) {
	pool := cartDB(t)
	catalog := &stubCatalog{products: map[string]*adapter.ProductInfo{}}
	uc := newUseCase(pool, catalog)
	buyer := uuid.NewString()

	ids := make([]string, domain.MaxLinesPerCart+10)
	for i := range ids {
		ids[i] = catalog.add(1000, "VND")
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	rejected := 0
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			err := uc.AddItem(context.Background(), buyer, id, nil, 1, nil)
			var appErr *apperror.Error
			if errors.As(err, &appErr) && appErr.Code == apperror.CodeValidation {
				mu.Lock()
				rejected++
				mu.Unlock()
			} else if err != nil {
				t.Error(err)
			}
		}(id)
	}
	wg.Wait()

	lines, version := viewQuantities(t, uc, buyer)
	if len(lines) != domain.MaxLinesPerCart || rejected != 10 {
		t.Fatalf("expected exactly %d lines and 10 rejections, got %d lines, %d rejected", domain.MaxLinesPerCart, len(lines), rejected)
	}
	if version != int64(domain.MaxLinesPerCart)+1 {
		t.Fatalf("every accepted add must bump the version once, got %d", version)
	}
}

func TestConsumeRacingWithCartEditsKeepsNewItems(t *testing.T) {
	pool := cartDB(t)
	catalog := &stubCatalog{products: map[string]*adapter.ProductInfo{}}
	uc := newUseCase(pool, catalog)
	ctx := t.Context()
	buyer := uuid.NewString()

	bought, raised := catalog.add(100, "VND"), catalog.add(200, "VND")
	for _, id := range []string{bought, raised} {
		if err := uc.AddItem(ctx, buyer, id, nil, 2, nil); err != nil {
			t.Fatal(err)
		}
	}
	operation := uuid.NewString()
	snap, _, err := uc.CreateCheckoutSnapshot(ctx, buyer, operation, nil)
	if err != nil {
		t.Fatal(err)
	}
	purchase := make([]domain.ConsumeLine, 0, len(snap.Lines))
	for _, l := range snap.Lines {
		purchase = append(purchase, domain.ConsumeLine{LineID: l.LineID, Quantity: l.Quantity})
	}

	newItems := make([]string, 15)
	for i := range newItems {
		newItems[i] = catalog.add(300, "VND")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 30)
	for _, id := range newItems {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			errs <- uc.AddItem(context.Background(), buyer, id, nil, 1, nil)
		}(id)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		errs <- uc.AddItem(context.Background(), buyer, raised, nil, 3, nil)
	}()
	for range 5 { // Order retrying the same consume concurrently.
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := uc.ConsumeCheckout(context.Background(), buyer, operation, purchase)
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

	lines, _ := viewQuantities(t, uc, buyer)
	if _, ok := lines[bought]; ok {
		t.Error("the purchased line must be gone")
	}
	if lines[raised] != 3 {
		t.Errorf("units added after the snapshot must stay, got %d", lines[raised])
	}
	for _, id := range newItems {
		if lines[id] != 1 {
			t.Errorf("a line added during checkout was lost: %s", id)
		}
	}

	var consumed int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM cart_checkout_operations WHERE consumed_at IS NOT NULL`).Scan(&consumed); err != nil || consumed != 1 {
		t.Fatalf("expected one receipt, got %d %v", consumed, err)
	}
}

func TestFailedConsumeRollsBackPartialChanges(t *testing.T) {
	pool := cartDB(t)
	catalog := &stubCatalog{products: map[string]*adapter.ProductInfo{}}
	uc := newUseCase(pool, catalog)
	ctx := t.Context()
	buyer := uuid.NewString()
	a, b := catalog.add(100, "VND"), catalog.add(100, "VND")
	for _, id := range []string{a, b} {
		if err := uc.AddItem(ctx, buyer, id, nil, 1, nil); err != nil {
			t.Fatal(err)
		}
	}
	operation := uuid.NewString()
	snap, _, err := uc.CreateCheckoutSnapshot(ctx, buyer, operation, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the stored receipt slot so MarkConsumed fails after the line
	// deletes already ran inside the transaction.
	if _, err := pool.Exec(ctx, `ALTER TABLE cart_checkout_operations ADD CONSTRAINT reject_receipts CHECK (receipt IS NULL) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	purchase := []domain.ConsumeLine{{LineID: snap.Lines[0].LineID, Quantity: 1}, {LineID: snap.Lines[1].LineID, Quantity: 1}}
	if _, _, err := uc.ConsumeCheckout(ctx, buyer, operation, purchase); err == nil {
		t.Fatal("expected the consume to fail")
	}
	lines, version := viewQuantities(t, uc, buyer)
	if len(lines) != 2 || version != snap.CartVersion {
		t.Fatalf("a failed consume must leave the cart untouched, got %v v%d", lines, version)
	}

	if _, err := pool.Exec(ctx, `ALTER TABLE cart_checkout_operations DROP CONSTRAINT reject_receipts`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := uc.ConsumeCheckout(ctx, buyer, operation, purchase); err != nil {
		t.Fatalf("the retry must succeed: %v", err)
	}
	if lines, _ := viewQuantities(t, uc, buyer); len(lines) != 0 {
		t.Fatalf("expected an empty cart after the retry, got %v", lines)
	}
}

func TestRetentionKeepsCartsWithPendingCheckout(t *testing.T) {
	pool := cartDB(t)
	catalog := &stubCatalog{products: map[string]*adapter.ProductInfo{}}
	uc := newUseCase(pool, catalog)
	ctx := t.Context()
	idle, pending, active := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, buyer := range []string{idle, pending, active} {
		if err := uc.AddItem(ctx, buyer, catalog.add(100, "VND"), nil, 1, nil); err != nil {
			t.Fatal(err)
		}
	}
	oldOperation := uuid.NewString()
	if _, _, err := uc.CreateCheckoutSnapshot(ctx, pending, oldOperation, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE carts SET updated_at = now() - interval '200 days' WHERE user_id = ANY($1::uuid[])`, []string{idle, pending}); err != nil {
		t.Fatal(err)
	}

	retention := repository.NewRetentionRepository(pool)
	n, err := retention.PurgeIdleCarts(ctx, time.Now().Add(-180*24*time.Hour), 100)
	if err != nil || n != 1 {
		t.Fatalf("expected only the idle cart without pending checkout to go, got %d %v", n, err)
	}
	var remaining []string
	rows, err := pool.Query(ctx, `SELECT user_id::text FROM carts ORDER BY user_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		remaining = append(remaining, id)
	}
	if len(remaining) != 2 || !slices.Contains(remaining, pending) || !slices.Contains(remaining, active) {
		t.Fatalf("unexpected remaining carts %v", remaining)
	}

	// Operations past their retention window are purged separately.
	if _, err := pool.Exec(ctx, `UPDATE cart_checkout_operations SET created_at = now() - interval '100 days'`); err != nil {
		t.Fatal(err)
	}
	if n, err := retention.PurgeOperations(ctx, time.Now().Add(-90*24*time.Hour), 100); err != nil || n != 1 {
		t.Fatalf("expected the old operation to be purged, got %d %v", n, err)
	}
}

func TestConsumeAfterCartWasPurgedTouchesNothing(t *testing.T) {
	pool := cartDB(t)
	catalog := &stubCatalog{products: map[string]*adapter.ProductInfo{}}
	uc := newUseCase(pool, catalog)
	ctx := t.Context()
	buyer := uuid.NewString()
	product := catalog.add(100, "VND")
	if err := uc.AddItem(ctx, buyer, product, nil, 1, nil); err != nil {
		t.Fatal(err)
	}
	operation := uuid.NewString()
	snap, _, err := uc.CreateCheckoutSnapshot(ctx, buyer, operation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM carts WHERE user_id = $1`, buyer); err != nil {
		t.Fatal(err)
	}
	if err := uc.AddItem(ctx, buyer, product, nil, 4, nil); err != nil {
		t.Fatal(err)
	}
	receipt, _, err := uc.ConsumeCheckout(ctx, buyer, operation, []domain.ConsumeLine{{LineID: snap.Lines[0].LineID, Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Lines[0].Outcome != domain.ConsumeAlreadyGone {
		t.Fatalf("unexpected outcome %+v", receipt.Lines)
	}
	if lines, _ := viewQuantities(t, uc, buyer); lines[product] != 4 {
		t.Fatalf("the new cart must be untouched, got %v", lines)
	}
}
