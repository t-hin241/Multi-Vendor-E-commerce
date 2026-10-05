package repository_test

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/repository"
)

func TestCheckoutSnapshotReadsPackagingVariantsLabelsAndMissingIDs(t *testing.T) {
	f := newFixture(t)
	p := f.product(t)
	plain := f.product(t)
	weight := int64(750)
	if err := repository.NewProductPackagingRepository(f.pool).Upsert(t.Context(), p.ID, domain.Packaging{WeightGrams: &weight}); err != nil {
		t.Fatal(err)
	}
	attr, opt := uuid.NewString(), uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO attributes(id,code,name,data_type,is_variant_defining) VALUES($1,'size','Size','select',true)`, attr); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO attribute_options(id,attribute_id,value) VALUES($1,$2,'L')`, opt, attr); err != nil {
		t.Fatal(err)
	}
	v := &domain.ProductVariant{ProductID: p.ID, SKU: "SKU-L", VariantKey: "size-L"}
	if err := repository.NewProductVariantRepository(f.pool).Create(t.Context(), v, []domain.VariantOptionSelection{{AttributeID: attr, OptionID: opt}}); err != nil {
		t.Fatal(err)
	}
	s, err := f.repo.ReadCheckout(t.Context(), []string{p.ID, plain.ID, p.ID, uuid.NewString()}, []string{v.ID, v.ID, uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Products) != 2 || len(s.Variants) != 1 {
		t.Fatalf("missing/duplicate IDs: %+v", s)
	}
	for _, got := range s.Products {
		old, hasVariants, w, err := f.uc.GetByIDForOwnerLookup(t.Context(), got.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Version != old.Version || got.EnforcedVersion != old.EnforcedVersion || got.PriceAmount != old.PriceAmount || got.VendorID != old.VendorID || got.HasVariants != hasVariants {
			t.Fatalf("batch differs from single lookup: %+v %+v", got, old)
		}
		if got.ID == p.ID && (w == nil || got.PackageWeightGrams == nil || *got.PackageWeightGrams != *w) {
			t.Fatal("wrong weight")
		}
		if got.ID == plain.ID && (got.HasVariants || got.PackageWeightGrams != nil) {
			t.Fatal("invented variant/weight")
		}
	}
	got := s.Variants[0]
	if got.ID != v.ID || got.ProductID != p.ID || got.VendorID != p.VendorID || got.SKU != "SKU-L" || len(got.Options) != 1 || got.Options[0].AttributeName != "Size" || got.Options[0].OptionValue != "L" {
		t.Fatalf("variant snapshot: %+v", got)
	}
	// A variant belonging to another requested product still reports its true
	// owner; Order must detect the mismatch, never reassign it by array position.
	s, err = f.repo.ReadCheckout(t.Context(), []string{plain.ID}, []string{v.ID})
	if err != nil || s.Variants[0].ProductID != p.ID {
		t.Fatalf("variant owner lost: %+v %v", s, err)
	}
}

func TestCheckoutSnapshotCannotMixFactsAcrossConcurrentCatalogEdits(t *testing.T) {
	f := newFixture(t)
	p := f.product(t)
	weight := p.PriceAmount
	if err := repository.NewProductPackagingRepository(f.pool).Upsert(t.Context(), p.ID, domain.Packaging{WeightGrams: &weight}); err != nil {
		t.Fatal(err)
	}
	v := &domain.ProductVariant{ProductID: p.ID, SKU: strconv.FormatInt(weight, 10), VariantKey: "plain"}
	if err := repository.NewProductVariantRepository(f.pool).Create(t.Context(), v, nil); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 101; i <= 150; i++ {
			err := pgx.BeginFunc(t.Context(), f.pool, func(tx pgx.Tx) error {
				if _, err := tx.Exec(t.Context(), `UPDATE products SET price_amount=$2 WHERE id=$1`, p.ID, i); err != nil {
					return err
				}
				if _, err := tx.Exec(t.Context(), `UPDATE product_packaging SET weight_grams=$2 WHERE product_id=$1`, p.ID, i); err != nil {
					return err
				}
				_, err := tx.Exec(t.Context(), `UPDATE product_variants SET sku=$2 WHERE id=$1`, v.ID, strconv.Itoa(i))
				return err
			})
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	// Always join the writer before the fixture drops its isolated schema.
	defer func() {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	for range 80 {
		s, err := f.repo.ReadCheckout(t.Context(), []string{p.ID}, []string{v.ID})
		if err != nil {
			t.Fatal(err)
		}
		product := s.Products[0]
		if product.PriceAmount != *product.PackageWeightGrams || s.Variants[0].SKU != fmt.Sprint(product.PriceAmount) {
			t.Fatalf("mixed snapshots: %+v %+v", product, s.Variants[0])
		}
	}
}
