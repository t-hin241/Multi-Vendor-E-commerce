package repository_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/productsales"
	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/repository"
	"shopee/backend/services/catalog/internal/usecase"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

func catalogDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("CATALOG_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("CATALOG_TEST_DATABASE_URL is not configured")
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
	schema := "catalog_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("migration %s: %v", filepath.Base(file), err)
		}
	}
	orderMigration, err := os.ReadFile("../../../order/migrations/000008_product_sale_status.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(orderMigration)); err != nil {
		t.Fatal(err)
	}
	return pool
}

type vendorStub struct{ owner, shop string }

func (v vendorStub) Approved(context.Context, []string) (map[string]int64, error) {
	return map[string]int64{v.shop: 1}, nil
}
func (v vendorStub) GetApprovedVendorID(_ context.Context, user, shop, _ string) (string, error) {
	if user != v.owner || shop != v.shop {
		return "", apperror.Forbidden("Not owner")
	}
	return shop, nil
}

type roleStub struct{ admin string }

func (r roleStub) RequireRole(_ context.Context, user, role string) error {
	if user != r.admin || role != "admin" {
		return apperror.Forbidden("Not admin")
	}
	return nil
}

type inventoryStub struct{}

func (inventoryStub) GetVariantStock(context.Context, []string) (map[string]int64, error) {
	return map[string]int64{}, nil
}
func (inventoryStub) CheckStockReadiness(context.Context, string, []string) (bool, error) {
	return true, nil
}
func (inventoryStub) GetProductStock(context.Context, string) (int64, bool, error) {
	return 10, true, nil
}

type objectStub struct {
	mu                sync.Mutex
	uploaded, deleted []string
	failDelete        bool
}

func (s *objectStub) Upload(_ context.Context, key string, _ []byte, _ string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploaded = append(s.uploaded, key)
	return "https://media.example.test/" + key, nil
}
func (s *objectStub) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failDelete {
		return errors.New("test storage unavailable")
	}
	s.deleted = append(s.deleted, key)
	return nil
}

type fixture struct {
	pool                         *pgxpool.Pool
	uc                           *usecase.ProductUseCase
	repo                         *repository.ProductRepository
	store                        *objectStub
	owner, shop, admin, category string
}

func newFixture(t *testing.T) *fixture {
	pool := catalogDB(t)
	owner, shop, admin := uuid.NewString(), uuid.NewString(), uuid.NewString()
	cats := repository.NewCategoryRepository(pool)
	cat := &domain.Category{Name: "Test", Slug: uuid.NewString(), Level: 1}
	if err := cats.Create(t.Context(), cat); err != nil {
		t.Fatal(err)
	}
	attrs := repository.NewAttributeRepository(pool)
	rules := repository.NewCategoryAttributeRuleRepository(pool)
	repo := repository.NewProductRepository(pool)
	store := &objectStub{}
	uc := usecase.NewProductUseCase(repo, repository.NewProductImageRepository(pool), repository.NewProductMediaRepository(pool), cats, repository.NewAuditLogRepository(pool), vendorStub{owner, shop}, store, nil, nil, repository.NewStorefrontCacheRepository(pool), usecase.NewAttributeUseCase(attrs, rules, cats, roleStub{admin}), repository.NewProductAttributeValueRepository(pool), repository.NewProductVariantRepository(pool), inventoryStub{}, repository.NewProductPackagingRepository(pool), usecase.Operations{Transactions: repository.Transactions{Pool: pool}, Cleanup: repository.ObjectCleanup{Pool: pool}, Identity: roleStub{admin}, Log: zerolog.Nop()})
	return &fixture{pool, uc, repo, store, owner, shop, admin, cat.ID}
}
func (f *fixture) product(t *testing.T) *domain.Product {
	t.Helper()
	p, err := f.uc.Create(t.Context(), f.owner, f.shop, f.category, "Product "+uuid.NewString(), "<p>Safe</p><script>bad()</script>", 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func jpegBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func (f *fixture) pending(t *testing.T) *domain.Product {
	p := f.product(t)
	if _, err := f.uc.UploadImage(t.Context(), f.owner, p.ID, "image/jpeg", jpegBytes(t)); err != nil {
		t.Fatal(err)
	}
	p, err := f.uc.SubmitForReview(t.Context(), f.owner, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCatalogModerationRollbackAndConcurrentDecision(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p := f.pending(t)
	_, err := f.pool.Exec(ctx, `CREATE FUNCTION fail_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test audit unavailable'; END $$; CREATE TRIGGER fail_audit BEFORE INSERT ON product_audit_logs FOR EACH ROW EXECUTE FUNCTION fail_audit()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.Approve(ctx, p.ID, f.admin); err == nil {
		t.Fatal("expected audit failure")
	}
	persisted, err := f.repo.FindByID(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != domain.StatusPendingReview || persisted.Version != p.Version {
		t.Fatal("failed audit changed product")
	}
	if _, err := f.pool.Exec(ctx, `DROP TRIGGER fail_audit ON product_audit_logs`); err != nil {
		t.Fatal(err)
	}
	outcomes := make(chan error, 2)
	go func() { _, err := f.uc.Approve(ctx, p.ID, f.admin); outcomes <- err }()
	go func() { _, err := f.uc.Reject(ctx, p.ID, f.admin, "Test rejection"); outcomes <- err }()
	successes := 0
	for i := 0; i < 2; i++ {
		if <-outcomes == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected one decision, got %d", successes)
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM product_audit_logs WHERE product_id=$1`, p.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one audit, got %d", count)
	}
	if _, err := f.uc.Approve(ctx, p.ID, f.owner); err == nil {
		t.Fatal("non-admin decision accepted")
	}
}

func TestCatalogMediaConcurrencyCleanupAndRevision(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p := f.pending(t)
	if _, err := f.uc.Approve(ctx, p.ID, f.admin); err != nil {
		t.Fatal(err)
	}
	data := jpegBytes(t)
	outcomes := make(chan error, 7)
	for i := 0; i < 7; i++ {
		go func() { _, err := f.uc.UploadMedia(ctx, f.owner, p.ID, "image/jpeg", data); outcomes <- err }()
	}
	successes := 0
	for i := 0; i < 7; i++ {
		if <-outcomes == nil {
			successes++
		}
	}
	if successes != 5 {
		t.Fatalf("expected five attachments, got %d", successes)
	}
	current, err := f.repo.FindByID(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domain.StatusDraft {
		t.Fatal("approved content edit did not require review")
	}
	cleanup := repository.ObjectCleanup{Pool: f.pool}
	orphan := "products/" + p.ID + "/unattached.jpg"
	if err := cleanup.Track(ctx, orphan, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := cleanup.Sweep(ctx, f.store); err != nil {
		t.Fatal(err)
	}
	if len(f.store.deleted) != 0 {
		t.Fatal("deleted before grace period")
	}
	if _, err := f.pool.Exec(ctx, `UPDATE catalog_object_cleanup SET next_attempt_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	f.store.failDelete = true
	if err := cleanup.Sweep(ctx, f.store); err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err := f.pool.QueryRow(ctx, `SELECT attempts FROM catalog_object_cleanup WHERE object_key=$1`, orphan).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatal("failed deletion not scheduled for retry")
	}
	f.store.failDelete = false
	if _, err := f.pool.Exec(ctx, `UPDATE catalog_object_cleanup SET next_attempt_at=now()`); err != nil {
		t.Fatal(err)
	}
	if err := cleanup.Sweep(ctx, f.store); err != nil {
		t.Fatal(err)
	}
	// Uploads that lost the race for the last slots are staged orphans too, so
	// the sweep may delete more than the explicit orphan, never referenced media.
	deletedOrphan := false
	for _, key := range f.store.deleted {
		deletedOrphan = deletedOrphan || key == orphan
		var referenced bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_images WHERE object_key=$1 UNION ALL SELECT 1 FROM product_media WHERE object_key=$1)`, key).Scan(&referenced); err != nil {
			t.Fatal(err)
		}
		if referenced {
			t.Fatal("cleanup deleted referenced media")
		}
	}
	if !deletedOrphan {
		t.Fatal("cleanup kept unattached object")
	}
	if _, err := f.uc.UploadImage(ctx, uuid.NewString(), p.ID, "image/jpeg", data); err == nil {
		t.Fatal("cross-owner upload allowed")
	}
}

func TestCatalogOutboxCheckoutFence(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p := f.pending(t)
	if _, err := f.uc.Approve(ctx, p.ID, f.admin); err != nil {
		t.Fatal(err)
	}
	store := productsales.Store{Pool: f.pool}
	queue := repository.StatusOutbox{Pool: f.pool, Publish: store.Apply}
	if err := queue.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := f.repo.FindByID(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != p.EnforcedVersion {
		t.Fatal("delivery not acknowledged")
	}
	checkout, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer checkout.Rollback(ctx)
	if err := productsales.LockVisible(ctx, checkout, map[string]int64{p.ID: p.Version}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.SetActive(ctx, f.owner, p.ID, false); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	queue.Publish = func(ctx context.Context, v productsales.Status) error { close(started); return store.Apply(ctx, v) }
	go func() { done <- queue.Dispatch(ctx) }()
	<-started
	select {
	case err := <-done:
		t.Fatalf("status update bypassed open checkout: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := checkout.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// A delayed old event cannot restore visibility after the newer unpublish.
	if err := store.Apply(ctx, productsales.Status{ProductID: p.ID, Version: p.Version, Visible: true}); err != nil {
		t.Fatal(err)
	}
	next, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Rollback(ctx)
	if err := productsales.LockVisible(ctx, next, map[string]int64{p.ID: p.Version}); err == nil {
		t.Fatal("stale checkout accepted after confirmed unpublish")
	}
}

func TestCatalogListingStableAndCacheExpiry(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	for i := 0; i < 6; i++ {
		p := f.product(t)
		if _, err := f.pool.Exec(ctx, `UPDATE products SET status='approved',created_at='2026-01-01',price_amount=100 WHERE id=$1`, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO vendor_sale_status(vendor_id,status,version) VALUES($1,'approved',1)`, f.shop); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for offset := 0; offset < 6; offset += 2 {
		items, err := f.repo.ListStorefront(ctx, "", f.shop, "", 2, offset, "price_asc")
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if seen[item.ID] {
				t.Fatal("duplicate between equal-sort pages")
			}
			seen[item.ID] = true
		}
	}
	if len(seen) != 6 {
		t.Fatal("missing listing results")
	}
	if _, err := f.pool.Exec(ctx, `UPDATE vendor_sale_status SET confirmed_at=now()-interval '61 seconds'`); err != nil {
		t.Fatal(err)
	}
	items, err := f.repo.ListStorefront(ctx, "", f.shop, "", 20, 0)
	if err != nil || len(items) != 0 {
		t.Fatal("stale selling permission leaked storefront items")
	}
	cache := repository.NewStorefrontCacheRepository(f.pool)
	variant := uuid.NewString()
	if err := cache.UpsertVariantStock(ctx, map[string]int64{variant: 9}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE variant_stock_cache SET updated_at=now()-interval '61 seconds'`); err != nil {
		t.Fatal(err)
	}
	stock, err := cache.GetVariantStock(ctx, []string{variant})
	if err != nil || len(stock) != 0 {
		t.Fatal("expired stock returned as cached availability")
	}
}

func TestCatalogVariantDatabaseConstraints(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p1, p2 := f.product(t), f.product(t)
	attrs := repository.NewAttributeRepository(f.pool)
	a := &domain.Attribute{Code: "size", Name: "Size", DataType: domain.DataTypeSelect, IsVariantDefining: true}
	b := &domain.Attribute{Code: "color", Name: "Color", DataType: domain.DataTypeSelect, IsVariantDefining: true}
	if err := attrs.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := attrs.Create(ctx, b); err != nil {
		t.Fatal(err)
	}
	option := &domain.AttributeOption{AttributeID: a.ID, Value: "Small"}
	if err := attrs.AddOption(ctx, option); err != nil {
		t.Fatal(err)
	}
	repo := repository.NewProductVariantRepository(f.pool)
	selections := []domain.VariantOptionSelection{{AttributeID: a.ID, OptionID: option.ID}}
	key := domain.BuildVariantKey(selections)
	if err := repo.Create(ctx, &domain.ProductVariant{ProductID: p1.ID, SKU: "TEST-SKU", VariantKey: key}, selections); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &domain.ProductVariant{ProductID: p2.ID, SKU: "TEST-SKU", VariantKey: key}, selections); !errors.Is(err, repository.ErrSKUTaken) {
		t.Fatalf("expected global SKU conflict: %v", err)
	}
	if err := repo.Create(ctx, &domain.ProductVariant{ProductID: p1.ID, SKU: "TEST-OTHER", VariantKey: key}, selections); !errors.Is(err, repository.ErrVariantAlreadyExists) {
		t.Fatalf("expected option combination conflict: %v", err)
	}
	mismatched := []domain.VariantOptionSelection{{AttributeID: b.ID, OptionID: option.ID}}
	if err := repo.Create(ctx, &domain.ProductVariant{ProductID: p2.ID, SKU: "TEST-BAD", VariantKey: domain.BuildVariantKey(mismatched)}, mismatched); err == nil {
		t.Fatal("mismatched option/attribute accepted")
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM product_variants`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("invalid variant left partial rows")
	}
}

func TestCatalogFailedMediaMetadataKeepsCleanupAndApproval(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p := f.pending(t)
	if _, err := f.uc.Approve(ctx, p.ID, f.admin); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `CREATE FUNCTION reject_media() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test metadata write failure'; END $$; CREATE TRIGGER reject_media BEFORE INSERT ON product_media FOR EACH ROW EXECUTE FUNCTION reject_media()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.UploadMedia(ctx, f.owner, p.ID, "image/jpeg", jpegBytes(t)); err == nil {
		t.Fatal("expected failed metadata write")
	}
	persisted, err := f.repo.FindByID(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != domain.StatusApproved {
		t.Fatal("failed upload changed moderation")
	}
	key := f.store.uploaded[len(f.store.uploaded)-1]
	var queued bool
	if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_object_cleanup WHERE object_key=$1)`, key).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if !queued {
		t.Fatal("uploaded orphan has no durable cleanup job")
	}
}

func TestCatalogRuleChangesAndCreateRollback(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p := f.product(t)
	attrs := repository.NewAttributeRepository(f.pool)
	rules := repository.NewCategoryAttributeRuleRepository(f.pool)
	uc := usecase.NewAttributeUseCase(attrs, rules, repository.NewCategoryRepository(f.pool), roleStub{f.admin})
	a, err := uc.CreateAttribute(ctx, f.admin, "material", "Material", domain.DataTypeText, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uc.SetCategoryRule(ctx, f.category, a.ID, true, false, 0, f.admin); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.UploadImage(ctx, f.owner, p.ID, "image/jpeg", jpegBytes(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.SubmitForReview(ctx, f.owner, p.ID); err == nil {
		t.Fatal("new required rule was bypassed by existing draft")
	}
	value := "Cotton"
	p, err = f.uc.UpdateContent(ctx, f.owner, p.ID, p.Name, p.Description, p.PriceAmount, p.Version, []usecase.AttributeValueInput{{AttributeID: a.ID, Value: &value}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.SubmitForReview(ctx, f.owner, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `CREATE FUNCTION reject_value() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test attribute write failure'; END $$; CREATE TRIGGER reject_value BEFORE INSERT ON product_attribute_values FOR EACH ROW EXECUTE FUNCTION reject_value()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.Create(ctx, f.owner, f.shop, f.category, "Failed creation", "", 100, []usecase.AttributeValueInput{{AttributeID: a.ID, Value: &value}}); err == nil {
		t.Fatal("expected attribute failure")
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM products`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("failed create left product behind")
	}
	if _, err := uc.AddOption(ctx, f.owner, a.ID, "bad"); err == nil {
		t.Fatal("non-admin attribute mutation allowed")
	}
}

func TestCatalogStatusRetryReplayAndResume(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p := f.product(t)
	outbox := repository.StatusOutbox{Pool: f.pool, Publish: func(context.Context, productsales.Status) error { return errors.New("test delivery unavailable") }}
	for i := 0; i < 10; i++ {
		if _, err := f.pool.Exec(ctx, `UPDATE product_status_outbox SET next_attempt_at=now()`); err != nil {
			t.Fatal(err)
		}
		if err := outbox.Dispatch(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := outbox.Dispatch(ctx); !errors.Is(err, repository.ErrNoProductStatus) {
		t.Fatalf("parked event retried: %v", err)
	}
	if err := outbox.Backfill(ctx); err != nil {
		t.Fatal(err)
	}
	ops := repository.Maintenance{Pool: f.pool}
	stats, err := ops.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats["status_parked"] != 1 || stats["status_delivery_failures"] != 10 {
		t.Fatal("parked event lost retry state")
	}
	if err := ops.Replay(ctx, f.admin, "status", p.ID, "Test dependency recovered"); err != nil {
		t.Fatal(err)
	}
	consumer := productsales.Store{Pool: f.pool}
	outbox.Publish = consumer.Apply
	if err := outbox.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	persisted, err := f.repo.FindByID(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.EnforcedVersion != persisted.Version {
		t.Fatal("replayed event not acknowledged")
	}
	var audits int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM product_audit_logs WHERE action='replay_status'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("missing replay audit: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE products SET enforced_version=0; DELETE FROM product_status_outbox`); err != nil {
		t.Fatal(err)
	}
	if err := outbox.Backfill(ctx); err != nil {
		t.Fatal(err)
	}
	if err := outbox.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if err := outbox.Backfill(ctx); err != nil {
		t.Fatal(err)
	}
	if err := outbox.Dispatch(ctx); !errors.Is(err, repository.ErrNoProductStatus) {
		t.Fatal("acknowledged product requeued")
	}
	for _, tableAndConstraint := range []string{"product_variant_options VALIDATE CONSTRAINT variant_option_attribute_fk", "product_attribute_values VALIDATE CONSTRAINT value_option_attribute_fk", "product_attribute_values VALIDATE CONSTRAINT value_exactly_one", "product_variants VALIDATE CONSTRAINT variant_sku_nonempty"} {
		if _, err := f.pool.Exec(ctx, "ALTER TABLE "+tableAndConstraint); err != nil {
			t.Fatal(err)
		}
	}
}
