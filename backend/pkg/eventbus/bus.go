package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/rs/zerolog"
)

// streamConfig is the one stream every service agrees on. Events are kept
// 14 days (replay window for a consumer that was down) on disk; the
// broker drops a republished event id seen in the last 10 minutes, and
// consumers' inboxes catch anything older.
func streamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:        StreamName,
		Description: "Shopee domain events (PLT-03)",
		Subjects:    []string{SubjectPrefix + ">"},
		Storage:     jetstream.FileStorage,
		Retention:   jetstream.LimitsPolicy,
		Discard:     jetstream.DiscardOld,
		MaxAge:      14 * 24 * time.Hour,
		MaxMsgSize:  MaxPayloadBytes + 4<<10,
		Duplicates:  10 * time.Minute,
		Replicas:    1,
	}
}

// Bus is one service's connection to the event stream.
type Bus struct {
	nc       *nats.Conn
	js       jetstream.JetStream
	log      zerolog.Logger
	mu       sync.Mutex
	ready    bool
	handlers map[string]Handler // durable name -> handler (replay)
}

// Credentials identify a service to the broker. The broker allows each
// service to publish only its own event types (shopee.events.<service>.>)
// and to use only its own durable consumers and reply inbox
// (deploy/nats/nats.conf). An empty Password connects anonymously (a test
// broker without authorization).
type Credentials struct {
	Service  string
	Password string
}

// Connect opens the connection; it does not fail when the broker is down
// (the service keeps serving and its outbox keeps the events), it keeps
// reconnecting in the background.
func Connect(url string, cred Credentials, log zerolog.Logger) (*Bus, error) {
	if cred.Service == "" {
		return nil, fmt.Errorf("eventbus: service name is required")
	}
	opts := []nats.Option{
		nats.Name(cred.Service),
		// Replies (publish acks, fetched messages) arrive on a prefix only
		// this service may subscribe to, so no other service can read them.
		nats.CustomInboxPrefix("_INBOX_" + cred.Service),
		nats.Timeout(5 * time.Second),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				log.Warn().Err(err).Msg("event_bus_disconnected")
			}
		}),
		nats.ReconnectHandler(func(*nats.Conn) { log.Info().Msg("event_bus_reconnected") }),
		// Asynchronous broker errors, such as a permissions violation when a
		// service publishes a type it does not own (the publish then times
		// out and the outbox keeps the event).
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			log.Error().Err(err).Msg("event_bus_error")
		}),
	}
	if cred.Password != "" {
		opts = append(opts, nats.UserInfo(cred.Service, cred.Password))
	}
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("eventbus: connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("eventbus: jetstream: %w", err)
	}
	return &Bus{nc: nc, js: js, log: log, handlers: map[string]Handler{}}, nil
}

// Connected reports whether the broker connection is up (operations
// report; not a readiness condition).
func (b *Bus) Connected() bool { return b.nc.IsConnected() }

// Close drains the connection: pending publishes and acks are flushed.
func (b *Bus) Close() {
	if err := b.nc.Drain(); err != nil {
		b.nc.Close()
	}
}

// ensureStream creates or updates the stream once per process.
func (b *Bus) ensureStream(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ready {
		return nil
	}
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := b.js.CreateOrUpdateStream(sctx, streamConfig()); err != nil {
		return fmt.Errorf("eventbus: ensure stream: %w", err)
	}
	b.ready = true
	return nil
}

// ErrBrokerUnavailable: the event was not acknowledged; the outbox keeps
// it and publishes it again (same event id) later.
var ErrBrokerUnavailable = errors.New("eventbus: broker did not acknowledge the event")

// Publish publishes env and returns once the broker stored it.
func (b *Bus) Publish(ctx context.Context, env Envelope) error {
	if err := env.Validate(); err != nil {
		return Permanent(err)
	}
	if err := b.ensureStream(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrBrokerUnavailable, err)
	}
	data, err := json.Marshal(env)
	if err != nil {
		return Permanent(err)
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := b.js.Publish(pctx, Subject(env.Type), data, jetstream.WithMsgID(env.EventID)); err != nil {
		return fmt.Errorf("%w: %v", ErrBrokerUnavailable, err)
	}
	return nil
}
