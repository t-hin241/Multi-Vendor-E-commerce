package repository_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"shopee/backend/pkg/noticeoutbox"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
	"shopee/backend/services/payment/internal/usecase"
)

// PW-009 (AF-06): the buyer is asked once for the account of each open
// refund that never had one, and told each time a given account is
// rejected; the notices carry the order id only and survive a failed relay.
func TestBuyerIsToldWhenARefundNeedsAnAccount(t *testing.T) {
	f := newManualFixture(t)
	ctx := t.Context()
	notices := repository.BuyerNotices{Pool: f.e.pool}
	f.uc.Notices = notices
	buyer := uuid.NewString()
	refund := f.refundFor(t, buyer, 1200)
	given := f.refundFor(t, buyer, 800)
	if _, err := f.uc.SubmitDestination(ctx, buyer, given.ID, fakeBeneficiary, 0); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if _, err := notices.QueueMissingDestinations(ctx, 100); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.e.count(t, `SELECT count(*) FROM payment_buyer_notices WHERE notice_type = 'refund_destination_needed' AND reference_id = $1 AND user_id = $2`,
		refund.OrderID, buyer); n != 1 {
		t.Fatalf("one request for the account, got %d", n)
	}
	if n := f.e.count(t, `SELECT count(*) FROM payment_buyer_notices WHERE reference_id = $1`, given.OrderID); n != 0 {
		t.Fatal("a refund with an account is not asked again")
	}

	reject := func(version int) {
		t.Helper()
		proof := f.auth.proof(f.approverA, domain.ProofPurposeDestinationDecide, domain.DestinationDecisionRef(given.ID, version, false))
		if _, err := f.uc.DecideDestination(ctx, f.approverA, given.ID, usecase.DestinationDecision{Version: version, Reason: "Name differs from the order", Proof: proof}); err != nil {
			t.Fatal(err)
		}
	}
	reject(1)
	if _, err := f.uc.SubmitDestination(ctx, buyer, given.ID, fakeBeneficiary, 1); err != nil {
		t.Fatal(err)
	}
	reject(2)
	if n := f.e.count(t, `SELECT count(*) FROM payment_buyer_notices WHERE notice_type = 'refund_destination_rejected' AND reference_id = $1`, given.OrderID); n != 2 {
		t.Fatalf("each rejected version is told, got %d", n)
	}
	if n := f.e.count(t, `SELECT count(*) FROM payment_buyer_notices WHERE dedup_key LIKE '%9704%' OR reference_id LIKE '%9704%'`); n != 0 {
		t.Fatal("a notice never carries the account")
	}

	outbox := notices.Outbox()
	down := errors.New("event bus down")
	if _, err := outbox.Drain(ctx, 10, func(context.Context, noticeoutbox.Notice) error { return down }); err != nil {
		t.Fatal(err)
	}
	if waiting, _, err := outbox.Pending(ctx); err != nil || waiting != 3 {
		t.Fatalf("failed relays are kept: %d %v", waiting, err)
	}
	if _, err := f.e.pool.Exec(ctx, `UPDATE payment_buyer_notices SET next_attempt_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	var sent []noticeoutbox.Notice
	if n, err := outbox.Drain(ctx, 10, func(_ context.Context, n noticeoutbox.Notice) error { sent = append(sent, n); return nil }); err != nil || n != 3 {
		t.Fatalf("relay: %d %v", n, err)
	}
	for _, n := range sent {
		if n.UserID != buyer || (n.ReferenceID != refund.OrderID && n.ReferenceID != given.OrderID) {
			t.Fatalf("notice to the buyer about the order: %+v", n)
		}
	}
}
