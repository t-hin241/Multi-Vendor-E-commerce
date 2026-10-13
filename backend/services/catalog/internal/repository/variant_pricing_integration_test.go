package repository_test

import (
	"context"
	"errors"
	"math"
	"os"
	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/purchasequote"
	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/repository"
	"testing"

	"github.com/google/uuid"
)

func TestVariantPriceBackfillAndRollbackGuards(t *testing.T) {
	pool := catalogDB(t, "000016")
	product, variant, vendor, category := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(t.Context(), `INSERT INTO categories(id,name,slug) VALUES($1,'Synthetic','synthetic-backfill')`, category); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO products(id,vendor_id,category_id,name,slug,description,price_amount,currency) VALUES($1,$2,$3,'Synthetic','synthetic-backfill','',123,'VND')`, product, vendor, category); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO product_variants(id,product_id,sku,variant_key) VALUES($1,$2,'SYNTHETIC-BACKFILL','synthetic')`, variant, product); err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../../migrations/000017_variant_pricing.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../migrations/000017_variant_pricing.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), string(up)); err != nil {
		t.Fatal(err)
	}
	var price, version int64
	var source string
	if err = pool.QueryRow(t.Context(), `SELECT v.price_amount,v.price_version,a.source FROM product_variants v JOIN variant_price_audit a ON a.variant_id=v.id WHERE v.id=$1`, variant).Scan(&price, &version, &source); err != nil {
		t.Fatal(err)
	}
	if price != 123 || version != 1 || source != "product_backfill_v1" {
		t.Fatal("backfill lost original price/provenance")
	}
	if _, err = pool.Exec(t.Context(), string(down)); err != nil {
		t.Fatalf("unused migration rollback: %v", err)
	}
	if _, err = pool.Exec(t.Context(), string(up)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), `UPDATE product_variants SET price_amount=150 WHERE id=$1`, variant); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), string(down)); err == nil {
		t.Fatal("differentiated SKU was lost in rollback")
	}
}

func pricedVariant(t *testing.T, f *fixture, p *domain.Product, amount int64) *domain.ProductVariant {
	t.Helper()
	v := &domain.ProductVariant{ProductID: p.ID, SKU: uuid.NewString(), VariantKey: uuid.NewString(), PriceAmount: amount}
	if err := repository.NewProductVariantRepository(f.pool).Create(t.Context(), v, nil, f.owner, "synthetic-price-test"); err != nil {
		t.Fatal(err)
	}
	return v
}
func sellPricedProduct(t *testing.T, f *fixture, id string) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), `UPDATE products SET status='approved',is_active=true WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE products SET enforced_version=version WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
}
func TestVariantPricingQuotesGateRangesAndOverflow(t *testing.T) {
	f := newFixture(t)
	p := f.product(t)
	a := pricedVariant(t, f, p, 120)
	b := pricedVariant(t, f, p, 250)
	plain := f.product(t)
	sellPricedProduct(t, f, p.ID)
	sellPricedProduct(t, f, plain.ID)
	inputs := []purchasequote.Input{{ProductID: p.ID, VariantID: &a.ID, Quantity: 2}, {ProductID: p.ID, VariantID: &b.ID, Quantity: 3}, {ProductID: plain.ID, Quantity: 1}}
	quote, err := f.uc.PurchaseQuotes(t.Context(), inputs)
	if err != nil {
		t.Fatal(err)
	}
	if quote.Lines[0].IsSellable || quote.Lines[1].IsSellable || !quote.Lines[2].IsSellable {
		t.Fatal("disabled capability must block differentiated SKUs only")
	}
	f.uc.VariantPricingEnabled = true
	quote, err = f.uc.PurchaseQuotes(t.Context(), inputs)
	if err != nil {
		t.Fatal(err)
	}
	if quote.Lines[0].UnitPrice != 120 || quote.Lines[1].UnitPrice != 250 || quote.Lines[2].UnitPrice != 100 || !quote.Lines[0].IsSellable || quote.Lines[0].VariantVersion != 1 || quote.QuoteVersion == "" {
		t.Fatalf("wrong prices: %+v", quote)
	}
	got, err := f.repo.FindByID(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MinPriceAmount == nil || *got.MinPriceAmount != 120 || *got.MaxPriceAmount != 250 {
		t.Fatalf("wrong range %+v", got)
	}
	// A foreign or deleted SKU is never replaced with the product price.
	bad, err := f.uc.PurchaseQuotes(t.Context(), []purchasequote.Input{{ProductID: plain.ID, VariantID: &a.ID, Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if bad.Lines[0].IsSellable || bad.Lines[0].UnitPrice != 0 {
		t.Fatal("foreign SKU fell back")
	}
	huge := pricedVariant(t, f, p, math.MaxInt64)
	_, err = f.uc.PurchaseQuotes(t.Context(), []purchasequote.Input{{ProductID: p.ID, VariantID: &huge.ID, Quantity: 2}})
	if err == nil {
		t.Fatal("overflow accepted")
	}
}

func TestDisabledCapabilityPropagatesLegacyProductPriceToStoredSKUs(t *testing.T) {
	f := newFixture(t)
	p := f.product(t)
	v := pricedVariant(t, f, p, 120)
	p, err := f.repo.FindByID(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := f.uc.UpdateContent(t.Context(), f.owner, p.ID, p.Name, p.Description, 175, p.Version, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err = repository.NewProductVariantRepository(f.pool).FindByID(t.Context(), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.PriceAmount != 175 || v.PriceAmount != 175 || v.PriceVersion != 2 {
		t.Fatalf("legacy product price did not propagate: product=%+v variant=%+v", updated, v)
	}
	var auditCount, outboxCount int
	if err = f.pool.QueryRow(t.Context(), `SELECT count(*) FROM variant_price_audit WHERE variant_id=$1 AND price_version=2`, v.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(t.Context(), `SELECT count(*) FROM variant_price_outbox WHERE variant_id=$1 AND price_version=2`, v.ID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 || outboxCount != 1 {
		t.Fatal("propagated price must keep audit and outbox")
	}
}

func TestVariantPriceCASAuditModerationOutboxAndRollback(t *testing.T) {
	f := newFixture(t)
	f.uc.VariantPricingEnabled = true
	p := f.product(t)
	v := pricedVariant(t, f, p, 120)
	sellPricedProduct(t, f, p.ID)
	before, _ := f.repo.FindByID(t.Context(), p.ID)
	if _, err := f.uc.UpdateVariantPrice(t.Context(), uuid.NewString(), p.ID, v.ID, 150, 1); err == nil {
		t.Fatal("cross-shop price write accepted")
	}
	outcomes := make(chan error, 2)
	for _, amount := range []int64{150, 180} {
		go func(amount int64) {
			_, err := f.uc.UpdateVariantPrice(t.Context(), f.owner, p.ID, v.ID, amount, 1)
			outcomes <- err
		}(amount)
	}
	successes := 0
	for range 2 {
		err := <-outcomes
		if err == nil {
			successes++
		} else {
			var app *apperror.Error
			if !errors.As(err, &app) || app.Code != "price_changed" {
				t.Fatalf("unexpected CAS error: %v", err)
			}
		}
	}
	if successes != 1 {
		t.Fatal("CAS allowed multiple writers")
	}
	now, err := f.repo.FindByID(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if now.Status != domain.StatusDraft || now.Version <= before.Version || now.EnforcedVersion == now.Version {
		t.Fatal("price bypassed moderation/version fence")
	}
	var old, amount, version int64
	var actor string
	if err := f.pool.QueryRow(t.Context(), `SELECT old_price,new_price,price_version,actor_user_id FROM variant_price_audit WHERE variant_id=$1 AND price_version=2`, v.ID).Scan(&old, &amount, &version, &actor); err != nil {
		t.Fatal(err)
	}
	if old != 120 || version != 2 || actor != f.owner || (amount != 150 && amount != 180) {
		t.Fatal("incorrect price audit")
	}
	if _, err := f.pool.Exec(t.Context(), `DELETE FROM variant_price_audit WHERE variant_id=$1`, v.ID); err == nil {
		t.Fatal("audit was mutable")
	}
	var event eventbus.Envelope
	outbox := repository.VariantPriceOutbox{Pool: f.pool, Publish: func(_ context.Context, e eventbus.Envelope) error {
		event = e
		return errors.New("synthetic broker unavailable")
	}}
	if err := outbox.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err := f.pool.QueryRow(t.Context(), `SELECT attempts FROM variant_price_outbox WHERE variant_id=$1`, v.ID).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("failed publish lost durable retry: %v", err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE variant_price_outbox SET next_attempt_at=now()`); err != nil {
		t.Fatal(err)
	}
	firstID := event.EventID
	outbox.Publish = func(_ context.Context, e eventbus.Envelope) error { event = e; return nil }
	if err := outbox.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if event.EventID != firstID || event.AggregateVersion != 2 || event.Type != "catalog.variant_price_changed" {
		t.Fatal("event retry changed identity")
	}
	// An audit failure must roll back the price and parent fence together.
	if _, err := f.pool.Exec(t.Context(), `CREATE FUNCTION fail_price_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic audit failure'; END$$;CREATE TRIGGER fail_price_audit BEFORE INSERT ON variant_price_audit FOR EACH ROW EXECUTE FUNCTION fail_price_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.UpdateVariantPrice(t.Context(), f.owner, p.ID, v.ID, 190, 2); err == nil {
		t.Fatal("audit failure accepted")
	}
	after, err := repository.NewProductVariantRepository(f.pool).FindByID(t.Context(), v.ID)
	if err != nil || after.PriceVersion != 2 || after.PriceAmount != amount {
		t.Fatalf("audit failure partially committed: %+v %v", after, err)
	}
}
