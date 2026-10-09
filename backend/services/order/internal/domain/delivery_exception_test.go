package domain

import (
	"testing"
	"time"
)

// AF-04: a case never leaves resolved, a redelivery and a refund are
// separate branches, and only a package back at the shop waits for goods.
func TestDeliveryExceptionTransitions(t *testing.T) {
	cases := []struct {
		from, to DeliveryExceptionStatus
		ok       bool
	}{
		{DXInvestigating, DXAwaitingGoods, true},
		{DXInvestigating, DXRefundPending, true},
		{DXInvestigating, DXAwaitingBuyer, false},
		{DXAwaitingGoods, DXAwaitingBuyer, true},
		{DXAwaitingBuyer, DXRedeliveryPending, true},
		{DXAwaitingBuyer, DXResolved, false},
		{DXRedeliveryPending, DXRefundPending, false},
		{DXRedeliveryPending, DXResolved, true},
		{DXRefundPending, DXAwaitingBuyer, false},
		{DXRefundPending, DXAwaitingGoods, true},
		{DXResolved, DXInvestigating, false},
		{DXResolved, DXNeedsReview, false},
	}
	for _, c := range cases {
		if got := CanTransitionDeliveryException(c.from, c.to); got != c.ok {
			t.Errorf("%s -> %s: got %v", c.from, c.to, got)
		}
	}
	if StatusForFact(FactReturned) != DXAwaitingGoods || StatusForFact(FactLost) != DXInvestigating || StatusForFact(FactAttemptsExhausted) != DXInvestigating {
		t.Fatal("returned waits for the goods; anything else is investigated")
	}
	if ValidExceptionFact(OutcomeDelivered) || !ValidExceptionFact(FactLost) {
		t.Fatal("Shipment reports failures only")
	}
	d := DeliveryException{CarrierOutcome: FactAttemptsExhausted}
	if d.CarrierFinal() {
		t.Fatal("a package still with the carrier may arrive: no refund yet")
	}
	d.CarrierOutcome = FactLost
	if !d.CarrierFinal() || d.GoodsBack() {
		t.Fatal("a lost package is final but never comes back")
	}
}

// Every unit is accounted for exactly once; nothing beyond the package.
func TestValidateReceipt(t *testing.T) {
	ordered := map[string]int64{"a": 2, "b": 1}
	ok := []ReceiptLine{{"a", ConditionSellable, 1}, {"a", ConditionDamaged, 1}, {"b", ConditionMissing, 1}}
	if err := ValidateReceipt(ok, ordered); err != nil {
		t.Fatal(err)
	}
	for name, lines := range map[string][]ReceiptLine{
		"short":       {{"a", ConditionSellable, 1}, {"b", ConditionSellable, 1}},
		"too many":    {{"a", ConditionSellable, 3}, {"b", ConditionSellable, 1}},
		"foreign":     {{"a", ConditionSellable, 2}, {"b", ConditionSellable, 1}, {"c", ConditionSellable, 1}},
		"condition":   {{"a", "lost", 2}, {"b", ConditionSellable, 1}},
		"duplicate":   {{"a", ConditionSellable, 1}, {"a", ConditionSellable, 1}, {"b", ConditionSellable, 1}},
		"empty":       {},
		"nonpositive": {{"a", ConditionSellable, 0}, {"a", ConditionSellable, 2}, {"b", ConditionSellable, 1}},
	} {
		if err := ValidateReceipt(lines, ordered); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	r := GoodsReceipt{Lines: ok}
	if r.AllSellable() || r.SellableByItem()["a"] != 1 || r.SellableByItem()["b"] != 0 {
		t.Fatalf("sellable units only: %+v", r.SellableByItem())
	}
	if DeliveryRecoveryID("x", "y") != "delivery_exception:x:y" {
		t.Fatal("recovery id names the case and item")
	}
}

// The deadline follows who must act; waiting on the buyer pauses it.
func TestDeliveryExceptionSLAStage(t *testing.T) {
	at := time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC)
	d := DeliveryException{ID: "dx", Status: DXAwaitingGoods, CreatedAt: at, UpdatedAt: at}
	if s := d.SLAStage(); s.Stage != "delivery_goods_receipt" || s.WaitingOn != "vendor" {
		t.Fatalf("no receipt: the shop acts: %+v", s)
	}
	d.Receipt = &GoodsReceipt{}
	if s := d.SLAStage(); s.Stage != "delivery_decision" || s.WaitingOn != "admin" {
		t.Fatalf("goods recorded: the marketplace decides: %+v", s)
	}
	d.Status = DXAwaitingBuyer
	if s := d.SLAStage(); !s.Pause || s.WaitingOn != "buyer" {
		t.Fatalf("waiting on the buyer pauses: %+v", s)
	}
	d.Status = DXRefundPending
	if s := d.SLAStage(); s.Stage != "" {
		t.Fatalf("a refund in progress follows its own deadline: %+v", s)
	}
}
