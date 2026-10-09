package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
)

// AF-08: a recorded payout result is kept for the shop in the same
// transaction (once), and relayed with references only until the broker
// takes it.
func TestPayoutResultIsToldToTheShopOnce(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	admin, vendor := uuid.NewString(), uuid.NewString()
	if _, err := e.settle.IngestVendorOrder(ctx, settlementOrder(vendor, time.Now().Add(-30*24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	batch, _, err := e.settle.CreatePayoutBatch(ctx, admin, "fake-notice-batch-1", "VND", nil)
	if err != nil || len(batch.Items) != 1 {
		t.Fatalf("batch %+v %v", batch, err)
	}
	notices := repository.VendorNotices{Pool: e.pool}
	e.settle.VendorNotices = notices
	item := batch.Items[0]
	if _, err := e.settle.ResolvePayoutItem(ctx, admin, item.ID, domain.PayoutResolution{Outcome: domain.PayoutItemFailed, Note: "account closed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.settle.ResolvePayoutItem(ctx, admin, item.ID, domain.PayoutResolution{Outcome: domain.PayoutItemFailed, Note: "account closed"}); err != nil {
		t.Fatalf("a replay of the same result is accepted: %v", err)
	}
	if _, err := e.settle.ResolvePayoutItem(ctx, admin, item.ID, domain.PayoutResolution{Outcome: domain.PayoutItemSucceeded, EvidenceReference: "FAKE-1"}); err == nil {
		t.Fatal("a resolved payout item cannot change its result")
	}
	if n := e.count(t, `SELECT count(*) FROM payment_vendor_notices WHERE payout_item_id = $1 AND outcome = 'failed' AND vendor_id = $2`, item.ID, vendor); n != 1 {
		t.Fatalf("expected one notice, got %d", n)
	}

	broker := errors.New("broker down")
	if err := notices.Dispatch(ctx, func(context.Context, repository.VendorNotice) error { return broker }); err != nil {
		t.Fatal(err)
	}
	if e.count(t, `SELECT count(*) FROM payment_vendor_notices WHERE delivered_at IS NULL AND attempts = 1 AND next_attempt_at > now()`) != 1 {
		t.Fatal("a failed relay must wait and retry")
	}
	if _, err := e.pool.Exec(ctx, `UPDATE payment_vendor_notices SET next_attempt_at = now()`); err != nil {
		t.Fatal(err)
	}
	var sent []repository.VendorNotice
	if err := notices.Dispatch(ctx, func(_ context.Context, n repository.VendorNotice) error { sent = append(sent, n); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].VendorID != vendor || sent[0].PayoutItemID != item.ID || sent[0].Outcome != "failed" {
		t.Fatalf("relayed %+v", sent)
	}
	if err := notices.Dispatch(ctx, func(context.Context, repository.VendorNotice) error { t.Fatal("relayed twice"); return nil }); !errors.Is(err, repository.ErrNoVendorNotice) {
		t.Fatalf("expected nothing left, got %v", err)
	}
	if waiting, review, err := notices.Pending(ctx); err != nil || waiting != 0 || review != 0 {
		t.Fatalf("pending %d %d %v", waiting, review, err)
	}
}
