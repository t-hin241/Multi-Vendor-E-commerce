package repository_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/repository"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func paymentDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("PAYMENT_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("PAYMENT_TEST_DATABASE_URL is not configured")
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
	schema := "payment_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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

func TestPaymentCaptureQueuesDurableInventoryReconciliation(t *testing.T) {
	pool := paymentDB(t)
	ctx := t.Context()
	id, order := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_intents(id,order_id,buyer_id,amount,currency,status,provider,provider_intent_id) VALUES($1,$2,$3,100,'VND','pending','test','fake-provider-reference')`, id, order, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='captured' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	var queued int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_order_sync`).Scan(&queued); err != nil || queued != 1 {
		t.Fatal("capture without durable delivery intent")
	}
	worker := repository.OrderSync{Pool: pool}
	if err := worker.Dispatch(ctx, func(context.Context, string, string) error { return errors.New("test order unavailable") }); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_order_sync SET next_attempt_at=now()`); err != nil {
		t.Fatal(err)
	}
	// Recreated worker sees the same pending intent after a simulated restart.
	if err := (repository.OrderSync{Pool: pool}).Dispatch(ctx, func(context.Context, string, string) error { return apperror.Conflict("Reservation expired") }); err != nil {
		t.Fatal(err)
	}
	var review bool
	var status string
	if err := pool.QueryRow(ctx, `SELECT s.requires_review,p.status FROM payment_order_sync s JOIN payment_intents p ON p.id=s.payment_intent_id WHERE p.id=$1`, id).Scan(&review, &status); err != nil {
		t.Fatal(err)
	}
	if !review || status != "captured" {
		t.Fatal("late capture was lost or silently reported fulfilled")
	}
	if err := worker.Dispatch(ctx, func(context.Context, string, string) error { t.Fatal("review case retried automatically"); return nil }); !errors.Is(err, repository.ErrNoOrderSync) {
		t.Fatal(err)
	}
}
func TestPaymentCaptureDeliveryIsRetriedAndAcknowledged(t *testing.T) {
	pool := paymentDB(t)
	ctx := t.Context()
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_intents(id,order_id,buyer_id,amount,currency,status,provider,provider_intent_id) VALUES($1,$2,$3,100,'VND','pending','test','fake-provider-reference')`, id, uuid.NewString(), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='captured';UPDATE payment_intents SET status='captured'`); err != nil {
		t.Fatal(err)
	}
	worker := repository.OrderSync{Pool: pool}
	calls := 0
	if err := worker.Dispatch(ctx, func(_ context.Context, _ string, outcome string) error {
		calls++
		if outcome != "captured" {
			t.Fatal("incorrect outcome")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := worker.Dispatch(ctx, func(context.Context, string, string) error { calls++; return nil }); !errors.Is(err, repository.ErrNoOrderSync) {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("acknowledged capture re-delivered")
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
