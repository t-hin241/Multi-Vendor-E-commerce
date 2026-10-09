package eventbus

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/rs/zerolog"
)

// These tests run against a broker started with deploy/nats/nats.conf
// (EVENTBUS_AUTH_TEST_NATS_URL), every service user having the password
// EVENTBUS_AUTH_TEST_PASSWORD. They prove that a service holding valid
// credentials still cannot forge another service's facts or read its
// deliveries.

func authBroker(t *testing.T) (string, string) {
	t.Helper()
	url, password := os.Getenv("EVENTBUS_AUTH_TEST_NATS_URL"), os.Getenv("EVENTBUS_AUTH_TEST_PASSWORD")
	if url == "" || password == "" {
		t.Skip("EVENTBUS_AUTH_TEST_NATS_URL and EVENTBUS_AUTH_TEST_PASSWORD are not configured")
	}
	return url, password
}

// lockedBuffer collects the bus log (the broker's asynchronous errors).
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func connectAs(t *testing.T, service string) (*Bus, *lockedBuffer) {
	t.Helper()
	url, password := authBroker(t)
	logs := &lockedBuffer{}
	bus, err := Connect(url, Credentials{Service: service, Password: password}, zerolog.New(logs))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.Close)
	deadline := time.Now().Add(10 * time.Second)
	for !bus.Connected() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not connect: %s", service, logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	return bus, logs
}

func eventOf(t *testing.T, eventType string) Envelope {
	t.Helper()
	env, err := New(uuid.NewString(), eventType, 1, uuid.NewString(), 1, map[string]string{"value": "x"})
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestBrokerRefusesUnknownClients(t *testing.T) {
	url, password := authBroker(t)
	for name, opts := range map[string][]nats.Option{
		"anonymous":      nil,
		"wrong password": {nats.UserInfo("order", "not-the-password")},
		"unknown user":   {nats.UserInfo("review", password)},
	} {
		nc, err := nats.Connect(url, opts...)
		if err == nil {
			nc.Close()
			t.Errorf("%s: connected", name)
		} else if !strings.Contains(strings.ToLower(err.Error()), "authorization") {
			t.Errorf("%s: unexpected error %v", name, err)
		}
	}
}

// Order holding valid credentials cannot publish a payment outcome (it
// would mark an order paid); Payment can. Notification publishes nothing.
func TestServicePublishesOnlyItsOwnEvents(t *testing.T) {
	order, orderLogs := connectAs(t, "order")
	payment, _ := connectAs(t, "payment")
	notification, _ := connectAs(t, "notification")
	ctx := t.Context()

	if err := order.Publish(ctx, eventOf(t, "order.fulfillment_ready")); err != nil {
		t.Fatalf("order publishing its own event: %v", err)
	}
	if err := payment.Publish(ctx, eventOf(t, "payment.outcome")); err != nil {
		t.Fatalf("payment publishing its own event: %v", err)
	}
	if err := order.Publish(ctx, eventOf(t, "payment.outcome")); err == nil {
		t.Fatal("order forged a payment outcome")
	}
	if !strings.Contains(orderLogs.String(), "Permissions Violation") {
		t.Fatalf("the refusal is logged as a permissions violation: %s", orderLogs.String())
	}
	if err := notification.Publish(ctx, eventOf(t, "order.notification_requested")); err == nil {
		t.Fatal("notification published an order event")
	}
}

func consumerConfig(durable, eventType string) jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{Durable: durable, FilterSubjects: []string{Subject(eventType)},
		AckPolicy: jetstream.AckExplicitPolicy, DeliverPolicy: jetstream.DeliverAllPolicy, AckWait: 60 * time.Second}
}

// A service reads only through its own durable consumers and acknowledges
// what it fetched; it cannot take over another service's consumer or listen
// to another service's inbox or to the raw event subjects.
func TestConsumerIsLimitedToItsOwnDurables(t *testing.T) {
	order, orderLogs := connectAs(t, "order")
	payment, _ := connectAs(t, "payment")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	env := eventOf(t, "payment.outcome")
	if err := payment.Publish(ctx, env); err != nil {
		t.Fatal(err)
	}
	cons, err := order.js.CreateOrUpdateConsumer(ctx, StreamName, consumerConfig("order-payment-outcomes", "payment.outcome"))
	if err != nil {
		t.Fatalf("order creating its own consumer: %v", err)
	}
	found := false
	for !found && ctx.Err() == nil {
		batch, err := cons.Fetch(10, jetstream.FetchMaxWait(2*time.Second))
		if err != nil {
			t.Fatalf("order fetching from its consumer: %v", err)
		}
		for msg := range batch.Messages() {
			var got Envelope
			if json.Unmarshal(msg.Data(), &got) == nil && got.EventID == env.EventID {
				found = true
			}
			// DoubleAck waits for the broker: it fails if the ack is refused.
			if err := msg.DoubleAck(ctx); err != nil {
				t.Fatalf("order acknowledging: %v", err)
			}
		}
	}
	if !found {
		t.Fatal("order did not receive the payment outcome")
	}

	sctx, scancel := context.WithTimeout(ctx, 5*time.Second)
	defer scancel()
	if _, err := order.js.CreateOrUpdateConsumer(sctx, StreamName, consumerConfig("payment-settlements", "order.vendor_order_settleable")); err == nil {
		t.Fatal("order took over a payment consumer")
	}

	for _, subject := range []string{"_INBOX_payment.>", "shopee.events.>"} {
		sub, err := order.nc.SubscribeSync(subject)
		if err == nil {
			_ = order.nc.Flush()
			_ = sub.Unsubscribe()
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) &&
		!(strings.Contains(orderLogs.String(), `Subscription to \"_INBOX_payment.>\"`) && strings.Contains(orderLogs.String(), `Subscription to \"shopee.events.>\"`)) {
		time.Sleep(50 * time.Millisecond)
	}
	logs := orderLogs.String()
	for _, subject := range []string{"_INBOX_payment.>", "shopee.events.>"} {
		if !strings.Contains(logs, `Subscription to \"`+subject+`\"`) {
			t.Errorf("subscribing to %s was not refused: %s", subject, logs)
		}
	}
}

// The real consumer path (inbox in PostgreSQL, ack after commit) works with
// a service's credentials.
func TestEventFlowsWithServiceCredentials(t *testing.T) {
	url, password := authBroker(t)
	payment, _ := connectAs(t, "payment")
	w := newWorldOn(t, url, Credentials{Service: "order", Password: password})
	w.typ = "payment.outcome"
	w.consume(t, "order-payment-outcomes", apply)
	env := w.event(t, "paid")
	if err := payment.Publish(t.Context(), env); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the outcome to be applied through order's consumer", func() bool {
		return w.count(t, `SELECT count(*) FROM applied WHERE event_id = $1`, env.EventID) == 1
	})
}

// PW-004: the event flows added by AF-02, AF-04 and AF-08 work end to end
// on the production broker configuration: the producer publishes its type,
// the consumer service reads it through its own durable and applies it
// once through the inbox (a redelivery with the same id is a duplicate),
// and events published while the consumer is down arrive after it starts
// again.
func TestAddFeatureEventFlowsWithServiceCredentials(t *testing.T) {
	url, password := authBroker(t)
	flows := []struct{ producer, consumer, durable, typ string }{
		{"vendor", "order", "order-policy-versions", "vendor.policy_published"},
		{"shipment", "order", "order-shipment-exceptions", "shipment.exception_detected"},
		{"order", "notification", "notification-vendor-actions", "order.vendor_action_required"},
		{"payment", "notification", "notification-vendor-actions", "payment.vendor_action_required"},
	}
	for _, f := range flows {
		t.Run(f.typ, func(t *testing.T) {
			producer, _ := connectAs(t, f.producer)
			w := newWorldOn(t, url, Credentials{Service: f.consumer, Password: password})
			w.typ = f.typ
			run := func() (stop func()) {
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan struct{})
				go func() {
					w.bus.Consume(ctx, w.inbox, Subscription{Durable: f.durable, Types: []string{f.typ}, Handle: apply})
					close(done)
				}()
				return func() { cancel(); <-done }
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = w.bus.js.DeleteConsumer(ctx, StreamName, f.durable)
			})

			stop := run()
			first := w.event(t, "first")
			for range 2 { // a relay retry republishes the same event id
				if err := producer.Publish(t.Context(), first); err != nil {
					t.Fatalf("%s publishing %s: %v", f.producer, f.typ, err)
				}
			}
			eventually(t, f.typ+" applied through "+f.durable, func() bool {
				return w.count(t, `SELECT count(*) FROM applied WHERE event_id = $1`, first.EventID) == 1
			})
			stop()

			later := []Envelope{w.event(t, "later-1"), w.event(t, "later-2")}
			for _, env := range later {
				if err := producer.Publish(t.Context(), env); err != nil {
					t.Fatal(err)
				}
			}
			stop = run()
			defer stop()
			eventually(t, "the backlog after the consumer restarts", func() bool {
				return w.count(t, `SELECT count(*) FROM applied WHERE event_id = ANY($1)`, []string{later[0].EventID, later[1].EventID}) == 2
			})
			if n := w.count(t, `SELECT count(*) FROM applied WHERE event_id = $1`, first.EventID); n != 1 {
				t.Fatalf("the first event was applied %d times", n)
			}
		})
	}
}
