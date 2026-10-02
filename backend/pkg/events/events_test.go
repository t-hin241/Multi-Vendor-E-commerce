package events

import (
	"encoding/json"
	"testing"
	"time"

	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/productsales"
	"shopee/backend/pkg/vendorsales"
)

// Contract tests (PLT-05): the JSON each event carries is pinned here.
// Changing a field name or meaning breaks a consumer on another release:
// add a field (consumers ignore unknown fields) or publish a new schema
// version that consumers accept first (CONTRACTS.md).

func pin(t *testing.T, env eventbus.Envelope, err error, wantType, wantAggregate, wantPayload string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", wantType, err)
	}
	if env.Type != wantType || env.SchemaVersion != V1 || env.AggregateID != wantAggregate || env.Producer == "" || env.OccurredAt.IsZero() {
		t.Fatalf("%s envelope: %+v", wantType, env)
	}
	var got, want any
	if err := json.Unmarshal(env.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(wantPayload), &want); err != nil {
		t.Fatalf("%s: bad golden: %v", wantType, err)
	}
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Fatalf("%s payload changed:\n got %s\nwant %s", wantType, g, w)
	}
}

func TestEventContracts(t *testing.T) {
	at := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	rule := int64(3)
	tracking := "VN123"
	vendor, product, order, vo := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333", "44444444-4444-4444-4444-444444444444"

	env, err := VendorStatus("row-1", vendorsales.Status{VendorID: vendor, Status: "approved", Version: 4})
	pin(t, env, err, VendorStatusChanged, vendor, `{"vendor_id":"`+vendor+`","status":"approved","version":4}`)
	if env.AggregateVersion != 4 || env.Producer != "vendor" {
		t.Fatal("vendor status carries its version")
	}

	env, err = ProductStatus("product-x", productsales.Status{ProductID: product, Version: 2, Visible: true})
	pin(t, env, err, ProductStatusChanged, product, `{"product_id":"`+product+`","version":2,"is_visible":true}`)

	env, err = VendorNotification("notice-1", vendor, NotificationRequest{UserID: "u-1", Type: "vendor_approved", ReferenceID: vendor})
	pin(t, env, err, VendorNotificationRequested, vendor, `{"user_id":"u-1","type":"vendor_approved","reference_id":"`+vendor+`"}`)

	env, err = OrderNotification("effect-1", order, NotificationRequest{UserID: "u-1", Type: "order_paid", ReferenceID: order})
	pin(t, env, err, OrderNotificationRequested, order, `{"user_id":"u-1","type":"order_paid","reference_id":"`+order+`"}`)

	env, err = StockChangedEvent([]string{product})
	pin(t, env, err, StockChanged, "stock", `{"variant_ids":["`+product+`"]}`)

	env, err = ReservationExpiredEvent(ReservationExpiry{ID: "evt-1", OrderID: order, Type: "ReservationExpired"})
	pin(t, env, err, ReservationExpired, order, `{"id":"evt-1","order_id":"`+order+`","type":"ReservationExpired"}`)

	env, err = FulfillmentReadyEvent("effect-2", order, Fulfillment{VendorOrderID: vo, VendorID: vendor, BuyerID: "b-1", PackageWeightGrams: 500,
		RecipientName: "R", Phone: "0900000000", Province: "HN", District: "D", Ward: "W", StreetAddress: "S",
		Quote: &QuotedFee{FeeAmount: 15000, CarrierID: "c", ZoneID: "z", FeeRuleID: "f"}})
	pin(t, env, err, FulfillmentReady, vo, `{"vendor_order_id":"`+vo+`","vendor_id":"`+vendor+`","buyer_id":"b-1","package_weight_grams":500,
		"recipient_name":"R","phone":"0900000000","province":"HN","district":"D","ward":"W","street_address":"S",
		"quote":{"fee_amount":15000,"carrier_id":"c","zone_id":"z","fee_rule_id":"f"}}`)

	env, err = FulfillmentCancelledEvent("effect-3", FulfillmentCancellation{VendorOrderID: vo})
	pin(t, env, err, FulfillmentCancelled, vo, `{"vendor_order_id":"`+vo+`"}`)

	env, err = VendorOrderSettleableEvent("effect-4", Settlement{VendorOrderID: vo, OrderID: order, VendorID: vendor, Currency: "VND",
		SubtotalAmount: 100000, ShippingAmount: 15000, CommissionAmount: 10000, CommissionRateBps: 1000, CommissionRuleVersion: &rule,
		CompletedAt: at, EligibleAt: at.Add(7 * 24 * time.Hour)})
	pin(t, env, err, VendorOrderSettleable, vo, `{"vendor_order_id":"`+vo+`","order_id":"`+order+`","vendor_id":"`+vendor+`","currency":"VND",
		"subtotal_amount":100000,"shipping_amount":15000,"commission_amount":10000,"commission_rate_bps":1000,"commission_rule_version":3,
		"completed_at":"2026-10-02T08:00:00Z","eligible_at":"2026-10-09T08:00:00Z"}`)

	env, err = ShipmentChangedEvent(ShipmentFact{EventID: "s-1", ShipmentID: "sh-1", VendorOrderID: vo, Type: "delivered", OccurredAt: at, TrackingNumber: &tracking})
	pin(t, env, err, ShipmentChanged, vo, `{"event_id":"s-1","shipment_id":"sh-1","vendor_order_id":"`+vo+`","type":"delivered",
		"occurred_at":"2026-10-02T08:00:00Z","tracking_number":"VN123"}`)

	env, err = PaymentOutcomeEvent(PaymentResult{PaymentID: "p-1", OrderID: order, Outcome: "captured", Amount: 115000, Currency: "VND"})
	pin(t, env, err, PaymentOutcome, order, `{"payment_id":"p-1","order_id":"`+order+`","outcome":"captured","amount":115000,"currency":"VND"}`)
	if env.EventID != "payment-p-1-captured" {
		t.Fatal("one event per payment and outcome")
	}

	env, err = RefundOutcomeEvent(RefundResult{OrderRefundID: "r-1", PaymentRefundID: "pr-1", Status: "succeeded", Amount: 5000, Currency: "VND"})
	pin(t, env, err, RefundOutcome, "r-1", `{"order_refund_id":"r-1","payment_refund_id":"pr-1","status":"succeeded","amount":5000,"currency":"VND"}`)

	env, err = OutcomeRejectedEvent("effect-5", OutcomeRejection{Kind: "payment", PaymentID: "p-1", OrderID: order, Reason: "amount differs"})
	pin(t, env, err, PaymentOutcomeRejected, order, `{"kind":"payment","payment_id":"p-1","order_id":"`+order+`","reason":"amount differs"}`)
}

// A consumer reads an older or newer payload without failing on fields
// it does not know (additive changes are compatible).
func TestConsumersIgnoreUnknownFields(t *testing.T) {
	env, _ := eventbus.New("x-1", VendorStatusChanged, V1, "v", 1, map[string]any{
		"vendor_id": "11111111-1111-1111-1111-111111111111", "status": "approved", "version": 1, "added_later": true})
	var s vendorsales.Status
	if err := env.Decode(&s); err != nil || !s.Valid() {
		t.Fatalf("decode: %+v %v", s, err)
	}
}
