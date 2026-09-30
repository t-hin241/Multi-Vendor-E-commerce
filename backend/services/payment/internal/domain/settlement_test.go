package domain

import (
	"testing"
	"time"
)

func settled() SettlementOrder {
	done := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return SettlementOrder{VendorOrderID: "vo-1", OrderID: "o-1", VendorID: "v-1", Currency: "VND", SubtotalAmount: 100000,
		ShippingAmount: 20000, CommissionAmount: 10000, CommissionRateBps: 1000, CompletedAt: done, EligibleAt: done.Add(7 * 24 * time.Hour)}
}

func sum(entries []Entry) int64 {
	var total int64
	for _, e := range entries {
		total += e.Amount
	}
	return total
}

func TestSaleEntriesCreditNetOfCommission(t *testing.T) {
	entries := SaleEntries(settled())
	if len(entries) != 3 || sum(entries) != 110000 {
		t.Fatalf("expected sale+shipping-commission = 110000, got %d in %d entries", sum(entries), len(entries))
	}
	for _, e := range entries {
		if !e.EligibleAt.Equal(settled().EligibleAt) {
			t.Error("sale entries become payable when the return window ends")
		}
	}
}

func TestRefundEntriesReverseCommissionOnItemsOnly(t *testing.T) {
	o := settled()
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	// A return of 30,000 of items: debit 30,000, give back 10% = 3,000.
	first := RefundEntries(o, "r1", 30000, now, 0, 0)
	if len(first) != 2 || first[0].Amount != -30000 || *first[0].BaseAmount != 30000 || first[1].Amount != 3000 {
		t.Fatalf("unexpected first refund entries %+v", first)
	}
	// A dispute refund of 90,000 (items 70,000 left + 20,000 shipping).
	second := RefundEntries(o, "r2", 90000, now, 30000, 3000)
	if second[0].Amount != -90000 || *second[0].BaseAmount != 70000 || second[1].Amount != 7000 {
		t.Fatalf("unexpected second refund entries %+v", second)
	}
	// Everything refunded: vendor keeps nothing, marketplace keeps nothing.
	all := append(append(SaleEntries(o), first...), second...)
	if sum(all) != 0 {
		t.Fatalf("a full refund must net to zero, got %d", sum(all))
	}
	// Nothing left to attribute: no further reversal.
	if extra := RefundEntries(o, "r3", 5000, now, 100000, 10000); len(extra) != 1 {
		t.Fatalf("no reversal once items are fully refunded, got %+v", extra)
	}
}

func TestRefundReversalRoundsDownAndIsCapped(t *testing.T) {
	o := settled()
	o.CommissionRateBps, o.CommissionAmount, o.SubtotalAmount = 333, 3, 100
	entries := RefundEntries(o, "r1", 99, time.Now(), 0, 0)
	if entries[1].Amount != 3 { // floor(99*333/10000) = 3
		t.Fatalf("expected floor rounding, got %+v", entries)
	}
	o.CommissionAmount = 2
	if capped := RefundEntries(o, "r2", 99, time.Now(), 0, 0); capped[1].Amount != 2 {
		t.Fatalf("reversal must not exceed the commission, got %+v", capped)
	}
}

func TestEntryPayable(t *testing.T) {
	now := time.Now()
	credit := Entry{Type: EntrySale, Amount: 100, EligibleAt: now.Add(time.Hour)}
	if credit.Payable(now, false) {
		t.Error("a credit inside the return window is not payable")
	}
	credit.EligibleAt = now.Add(-time.Hour)
	if !credit.Payable(now, false) || credit.Payable(now, true) {
		t.Error("an eligible credit is payable unless its order is held")
	}
	debit := Entry{Type: EntryRefund, Amount: -50, EligibleAt: now.Add(time.Hour)}
	if !debit.Payable(now, true) {
		t.Error("refunds are always netted")
	}
	commission := Entry{Type: EntryCommission, Amount: -10, EligibleAt: now.Add(time.Hour)}
	if commission.Payable(now, false) {
		t.Error("commission follows its sale's eligibility")
	}
	if (Entry{Type: EntryPayout, Amount: -100}).Payable(now, false) {
		t.Error("payout entries are never paid again")
	}
}

func TestResolvePayoutItem(t *testing.T) {
	item := &PayoutItem{Status: PayoutItemPending}
	if _, err := ResolvePayoutItem(item, PayoutResolution{Outcome: PayoutItemSucceeded}, "a", time.Now()); err == nil {
		t.Fatal("a paid transfer needs its bank reference")
	}
	changed, err := ResolvePayoutItem(item, PayoutResolution{Outcome: PayoutItemSucceeded, EvidenceReference: "FAKE-REF"}, "a", time.Now())
	if err != nil || !changed || item.Status != PayoutItemSucceeded {
		t.Fatalf("unexpected %v %v", changed, err)
	}
	if changed, _ := ResolvePayoutItem(item, PayoutResolution{Outcome: PayoutItemSucceeded, EvidenceReference: "FAKE-REF"}, "a", time.Now()); changed {
		t.Fatal("repeating the result is a no-op")
	}
	if _, err := ResolvePayoutItem(item, PayoutResolution{Outcome: PayoutItemFailed, Note: "bounced"}, "a", time.Now()); err == nil {
		t.Fatal("a paid transfer cannot become failed")
	}
}

func TestSettlementOrderValidation(t *testing.T) {
	o := settled()
	o.CommissionAmount = o.SubtotalAmount + 1
	if o.Validate() == nil {
		t.Error("commission above the subtotal must be refused")
	}
	o = settled()
	o.EligibleAt = o.CompletedAt.Add(-time.Second)
	if o.Validate() == nil {
		t.Error("eligible_at before completion must be refused")
	}
}
