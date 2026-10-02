package repository_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/services/payment/internal/repository"
	"shopee/backend/services/payment/internal/transport"
)

// PLT-03: a capture published as an event is delivered once the broker
// stored it; when Order refuses it (order.payment_outcome_rejected) the
// outcome goes up for review again, as an HTTP refusal did.
func TestRejectedOutcomeGoesUpForReview(t *testing.T) {
	pool := paymentDB(t)
	ctx := t.Context()
	id, order := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_intents(id,order_id,buyer_id,amount,currency,status,provider,provider_intent_id)
		VALUES($1,$2,$3,100,'VND','pending','test','fake-provider-reference')`, id, order, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='captured' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	sync := repository.OrderSync{Pool: pool}
	var published []events.PaymentResult
	if err := sync.Dispatch(ctx, func(_ context.Context, out repository.OrderOutcome) error {
		env, err := events.PaymentOutcomeEvent(events.PaymentResult{PaymentID: out.PaymentID, OrderID: out.OrderID, Outcome: out.Outcome, Amount: out.Amount, Currency: out.Currency})
		if err != nil {
			return err
		}
		var p events.PaymentResult
		if err := env.Decode(&p); err != nil {
			return err
		}
		published = append(published, p)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(published) != 1 || published[0].Amount != 100 || published[0].OrderID != order {
		t.Fatalf("published %+v", published)
	}
	pending, review, _ := sync.Counts(ctx)
	if pending != 0 || review != 0 {
		t.Fatal("published means delivered")
	}

	env, err := events.OutcomeRejectedEvent("effect-1", events.OutcomeRejection{Kind: "payment", PaymentID: id, OrderID: order, Reason: "amount differs"})
	if err != nil {
		t.Fatal(err)
	}
	handle := transport.OutcomeRejectedHandler(sync, repository.RefundSync{Pool: pool})
	inbox := eventbus.Inbox{Pool: pool}
	for i := 0; i < 2; i++ {
		if err := inbox.Process(ctx, "payment-rejected-outcomes", env, handle); err != nil {
			t.Fatal(err)
		}
	}
	if _, review, _ = sync.Counts(ctx); review != 1 {
		t.Fatalf("expected the outcome up for review, got %d", review)
	}
	problems, err := sync.ListProblems(ctx, 10)
	if err != nil || len(problems) != 1 || problems[0].LastError == nil || *problems[0].LastError != "order_rejected_outcome" {
		t.Fatalf("problems %+v %v", problems, err)
	}
	bad, _ := events.OutcomeRejectedEvent("effect-2", events.OutcomeRejection{Kind: "bogus", OrderID: order, Reason: "x"})
	if err := inbox.Process(ctx, "payment-rejected-outcomes", bad, handle); err == nil || !eventbus.IsPermanent(err) {
		t.Fatalf("an unknown kind is refused for good, got %v", err)
	}
}
