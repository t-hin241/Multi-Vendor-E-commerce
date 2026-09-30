package repository_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
)

func capturedIntent(t *testing.T, pool *pgxpool.Pool, order string, amount int64) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(t.Context(), `INSERT INTO payment_intents(id,order_id,buyer_id,amount,currency,status,provider,provider_intent_id) VALUES($1,$2,$3,$4,'VND','captured','test',$5)`,
		id, order, uuid.NewString(), amount, "fake-ref-"+id); err != nil {
		t.Fatal(err)
	}
	return id
}

func refundReq(order string, amount int64) domain.RefundRequest {
	return domain.RefundRequest{OrderRefundID: uuid.NewString(), OrderID: order, Amount: amount, Currency: "VND", Reason: "return", RequestedBy: uuid.NewString()}
}

func check(req domain.RefundRequest) func(*domain.PaymentIntent, int64) error {
	return func(intent *domain.PaymentIntent, committed int64) error {
		return domain.CheckRefundable(intent, committed, req)
	}
}

func TestConcurrentRefundsNeverExceedTheCapture(t *testing.T) {
	pool := paymentDB(t)
	repo := repository.NewRefundRepository(pool)
	order := uuid.NewString()
	capturedIntent(t, pool, order, 1000)

	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := refundReq(order, 300)
			if _, created, err := repo.Request(context.Background(), req, check(req)); err == nil && created {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if accepted != 3 {
		t.Fatalf("expected exactly 3 refunds of 300 against 1000, got %d", accepted)
	}
}

func TestRefundRequestIsIdempotentAndResolutionIsQueuedForOrder(t *testing.T) {
	pool := paymentDB(t)
	ctx := t.Context()
	repo := repository.NewRefundRepository(pool)
	order := uuid.NewString()
	intent := capturedIntent(t, pool, order, 1000)

	req := refundReq(order, 1000)
	first, created, err := repo.Request(ctx, req, check(req))
	if err != nil || !created || first.Status != domain.RefundAwaitingProvider || first.PaymentIntentID != intent {
		t.Fatalf("unexpected first request %+v %v", first, err)
	}
	again, created, err := repo.Request(ctx, req, check(req))
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("replay must return the stored refund: %+v %v", again, err)
	}
	changed := req
	changed.Amount = 999
	var app *apperror.Error
	if _, _, err := repo.Request(ctx, changed, check(changed)); !errors.As(err, &app) || app.Code != apperror.CodeConflict {
		t.Fatalf("a different refund under the same id must conflict, got %v", err)
	}

	// Awaiting refunds are not deliverable yet.
	sync := repository.RefundSync{Pool: pool}
	if err := sync.Dispatch(ctx, func(context.Context, domain.RefundOutcome) error { t.Fatal("unresolved refund delivered"); return nil }); !errors.Is(err, repository.ErrNoRefundSync) {
		t.Fatal(err)
	}

	admin := uuid.NewString()
	resolved, err := repo.Resolve(ctx, first.ID, func(r *domain.Refund) (bool, error) {
		return r.Resolve(domain.RefundResolution{Outcome: domain.RefundSucceeded, EvidenceReference: "FAKE-BANK-REF"}, admin, time.Now())
	})
	if err != nil || resolved.Status != domain.RefundSucceeded {
		t.Fatalf("resolve: %+v %v", resolved, err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM payment_intents WHERE id=$1`, intent).Scan(&status); err != nil || status != "refunded" {
		t.Fatalf("a fully refunded capture must be marked refunded, got %q", status)
	}

	// Order briefly unavailable, then acknowledges once.
	if err := sync.Dispatch(ctx, func(context.Context, domain.RefundOutcome) error { return errors.New("test order unavailable") }); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_refund_sync SET next_attempt_at=now()`); err != nil {
		t.Fatal(err)
	}
	var got domain.RefundOutcome
	if err := sync.Dispatch(ctx, func(_ context.Context, out domain.RefundOutcome) error { got = out; return nil }); err != nil {
		t.Fatal(err)
	}
	if got.OrderRefundID != req.OrderRefundID || got.PaymentRefundID != first.ID || got.Status != "succeeded" || got.Amount != 1000 || got.Currency != "VND" {
		t.Fatalf("unexpected outcome %+v", got)
	}
	if err := sync.Dispatch(ctx, func(context.Context, domain.RefundOutcome) error {
		t.Fatal("acknowledged outcome re-delivered")
		return nil
	}); !errors.Is(err, repository.ErrNoRefundSync) {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_refunds SET evidence_reference=NULL WHERE id=$1`, first.ID); err == nil {
		t.Fatal("the database must refuse a succeeded Order refund without evidence")
	}
}

func TestRefundNeedsAPaymentPinWhenAnOrderWasCapturedTwice(t *testing.T) {
	pool := paymentDB(t)
	ctx := t.Context()
	repo := repository.NewRefundRepository(pool)
	order := uuid.NewString()
	capturedIntent(t, pool, order, 500)
	duplicate := capturedIntent(t, pool, order, 500)

	req := refundReq(order, 500)
	var app *apperror.Error
	if _, _, err := repo.Request(ctx, req, check(req)); !errors.As(err, &app) || app.Code != apperror.CodeConflict {
		t.Fatalf("an ambiguous capture must be refused, got %v", err)
	}
	req.PaymentID = &duplicate
	refund, _, err := repo.Request(ctx, req, check(req))
	if err != nil || refund.PaymentIntentID != duplicate {
		t.Fatalf("pinned refund: %+v %v", refund, err)
	}
	// A failed resolution releases the amount and is still reported.
	if _, err := repo.Resolve(ctx, refund.ID, func(r *domain.Refund) (bool, error) {
		return r.Resolve(domain.RefundResolution{Outcome: domain.RefundFailed, Note: "account closed"}, uuid.NewString(), time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	retry := refundReq(order, 500)
	retry.PaymentID = &duplicate
	if _, created, err := repo.Request(ctx, retry, check(retry)); err != nil || !created {
		t.Fatalf("a failed refund must not hold the captured amount: %v", err)
	}
	var review bool
	if err := (repository.RefundSync{Pool: pool}).Dispatch(ctx, func(_ context.Context, out domain.RefundOutcome) error {
		if out.Status != "failed" || out.FailureReason != "account closed" {
			t.Errorf("unexpected failed outcome %+v", out)
		}
		return apperror.Conflict("Order refused")
	}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT requires_review FROM payment_refund_sync WHERE payment_refund_id=$1`, refund.ID).Scan(&review); err != nil || !review {
		t.Fatal("an outcome Order refuses must be parked for review")
	}
	list, total, err := repo.List(ctx, "open", 10, 0)
	if err != nil || total != 1 || len(list) != 1 || list[0].ID == refund.ID {
		t.Fatalf("open queue: %d %v", total, err)
	}
}
