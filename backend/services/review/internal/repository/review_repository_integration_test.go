package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestListVendorIntegration(t *testing.T) {
	raw := os.Getenv("REVIEW_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("REVIEW_TEST_DATABASE_URL is not configured")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal("invalid integration database configuration")
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("integration database name must end with _test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("connect integration database")
	}
	defer admin.Close()
	schema := "review_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	cfg = cfg.Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("connect isolated review schema")
	}
	defer pool.Close()
	migration, err := os.ReadFile("../../migrations/000001_review_core.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	vendor, otherVendor := uuid.NewString(), uuid.NewString()
	productA, productB := uuid.NewString(), uuid.NewString()
	first, second := uuid.NewString(), uuid.NewString()
	for i, item := range []struct {
		id, vendor, product, status string
		rating                      int
	}{
		{first, vendor, productA, "published", 5},
		{second, vendor, productB, "published", 3},
		{uuid.NewString(), vendor, productA, "hidden", 1},
		{uuid.NewString(), otherVendor, productA, "published", 4},
	} {
		_, err := pool.Exec(ctx, `INSERT INTO reviews(id,buyer_id,vendor_id,product_id,order_item_id,vendor_order_id,rating,comment,status,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,'Integration test review',$8,$9)`, item.id, uuid.NewString(), item.vendor, item.product, uuid.NewString(), uuid.NewString(), item.rating, item.status, time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO review_replies(review_id,vendor_id,message) VALUES($1,$2,'Test reply')`, first, vendor); err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	repo := NewReviewRepository(pool)
	for _, tc := range []struct {
		name, product string
		rating        int
		replied       *bool
		limit, offset int
		want          []string
	}{
		{"all products", "", 0, nil, 20, 0, []string{second, first}},
		{"product filter", productA, 0, nil, 20, 0, []string{first}},
		{"unknown product", uuid.NewString(), 0, nil, 20, 0, nil},
		{"rating filter", "", 3, nil, 20, 0, []string{second}},
		{"replied", "", 0, &yes, 20, 0, []string{first}},
		{"unreplied", "", 0, &no, 20, 0, []string{second}},
		{"pagination", "", 0, nil, 1, 1, []string{first}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items, err := repo.ListVendor(ctx, vendor, tc.product, tc.rating, tc.replied, tc.limit, tc.offset)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != len(tc.want) {
				t.Fatalf("got %d reviews, want %d", len(items), len(tc.want))
			}
			for i, item := range items {
				if item.ID != tc.want[i] || item.VendorID != vendor || string(item.Status) != "published" {
					t.Fatalf("unexpected review at index %d", i)
				}
			}
		})
	}
}
