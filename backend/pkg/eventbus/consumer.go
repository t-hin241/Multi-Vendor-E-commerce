package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/codes"

	"shopee/backend/pkg/middleware"
)

// MaxAttempts is how many deliveries a failing event gets before it is
// parked (plan default: 5 with backoff and jitter).
const MaxAttempts = 5

// Subscription is one durable consumer of a service.
type Subscription struct {
	// Durable is the consumer name, unique per service and purpose
	// ("order-payment-outcomes"); it is also the inbox's consumer key.
	Durable string
	// Types are the event types it receives.
	Types  []string
	Handle Handler
}

// retryDelay is the redelivery delay (tests shorten it).
var retryDelay = Backoff

// Backoff is the wait before delivery attempt n+1 (n >= 1): 2s, 8s, 32s,
// 128s with ±20% jitter.
func Backoff(n int) time.Duration {
	base := 2 * time.Second
	for i := 1; i < n && i < 4; i++ {
		base *= 4
	}
	jitter := 0.8 + rand.Float64()*0.4
	return time.Duration(float64(base) * jitter)
}

// Consume runs the subscriptions until ctx ends. Each message is handled
// one at a time: decoded, applied through the inbox (exactly once per
// consumer), and acknowledged only after that commit. A message being
// handled when ctx ends is finished first (shutdown drain).
func (b *Bus) Consume(ctx context.Context, inbox Inbox, subs ...Subscription) {
	for _, s := range subs {
		b.mu.Lock()
		b.handlers[s.Durable] = s.Handle
		b.mu.Unlock()
	}
	done := make(chan struct{}, len(subs))
	for _, s := range subs {
		go func(s Subscription) {
			defer func() { done <- struct{}{} }()
			b.run(ctx, inbox, s)
		}(s)
	}
	for range subs {
		<-done
	}
}

// HandlerFor returns the handler of a durable consumer (replay).
func (b *Bus) HandlerFor(durable string) (Handler, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	h, ok := b.handlers[durable]
	return h, ok
}

func (b *Bus) run(ctx context.Context, inbox Inbox, s Subscription) {
	log := b.log.With().Str("consumer", s.Durable).Logger()
	subjects := make([]string, 0, len(s.Types))
	for _, t := range s.Types {
		subjects = append(subjects, Subject(t))
	}
	var cons jetstream.Consumer
	for ctx.Err() == nil {
		if cons == nil {
			err := b.ensureStream(ctx)
			if err == nil {
				cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				cons, err = b.js.CreateOrUpdateConsumer(cctx, StreamName, jetstream.ConsumerConfig{
					Durable:        s.Durable,
					FilterSubjects: subjects,
					AckPolicy:      jetstream.AckExplicitPolicy,
					DeliverPolicy:  jetstream.DeliverAllPolicy,
					// The handler's transaction is bounded to 30s.
					AckWait:       60 * time.Second,
					MaxDeliver:    -1, // parking is decided here, with the reason
					MaxAckPending: 64,
				})
				cancel()
			}
			if err != nil {
				log.Warn().Err(err).Msg("event_consumer_unavailable")
				sleep(ctx, 5*time.Second)
				continue
			}
		}
		batch, err := cons.Fetch(10, jetstream.FetchMaxWait(5*time.Second))
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Warn().Err(err).Msg("event_fetch_failed")
				cons = nil
				sleep(ctx, 2*time.Second)
			}
			continue
		}
		for msg := range batch.Messages() {
			b.handle(ctx, inbox, s, msg)
		}
		if err := batch.Error(); err != nil && !errors.Is(err, jetstream.ErrNoMessages) && ctx.Err() == nil {
			log.Warn().Err(err).Msg("event_fetch_interrupted")
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// handle applies one message; the work itself is not cut short by
// shutdown (bounded by the inbox transaction timeout).
func (b *Bus) handle(ctx context.Context, inbox Inbox, s Subscription, msg jetstream.Msg) {
	start := time.Now()
	result := "ack"
	defer func() { handleDuration.WithLabelValues(s.Durable, result).Observe(time.Since(start).Seconds()) }()
	work := context.WithoutCancel(ctx)
	attempt := 1
	seq := "0"
	if md, err := msg.Metadata(); err == nil {
		attempt = int(md.NumDelivered)
		seq = strconv.FormatUint(md.Sequence.Stream, 10)
	}
	log := b.log.With().Str("consumer", s.Durable).Int("attempt", attempt).Logger()
	var env Envelope
	if err := json.Unmarshal(msg.Data(), &env); err != nil || env.Validate() != nil {
		// Unreadable: kept under its stream sequence so it can be inspected.
		bad := Envelope{EventID: "stream-seq-" + seq, Type: "invalid.envelope", SchemaVersion: 1, AggregateID: "unknown",
			Payload: json.RawMessage(`{}`)}
		if parkErr := inbox.Park(work, s.Durable, bad, attempt, "malformed envelope"); parkErr != nil {
			log.Error().Err(parkErr).Msg("event_park_failed")
			result = "retry"
			_ = msg.NakWithDelay(retryDelay(attempt))
			return
		}
		log.Error().Str("event_id", bad.EventID).Msg("event_parked_malformed")
		result = "parked"
		_ = msg.Ack()
		return
	}
	log = log.With().Str("event_id", env.EventID).Str("type", env.Type).Logger()
	if attempt == 1 {
		observeLag(s.Durable, env.OccurredAt)
	}
	hctx := middleware.ContextWithRequestID(work, firstNonEmpty(env.CorrelationID, env.EventID))
	hctx, span := startProcessSpan(hctx, s.Durable, msg.Headers(), env)
	defer span.End()
	err := inbox.Process(hctx, s.Durable, env, s.Handle)
	if err == nil {
		if ackErr := msg.Ack(); ackErr != nil {
			// Applied and committed: a redelivery is a duplicate for the inbox.
			log.Warn().Err(ackErr).Msg("event_ack_failed")
		}
		return
	}
	span.SetStatus(codes.Error, "not applied")
	if IsPermanent(err) || attempt >= MaxAttempts {
		if parkErr := inbox.Park(hctx, s.Durable, env, attempt, err.Error()); parkErr != nil {
			log.Error().Err(parkErr).Msg("event_park_failed")
			result = "retry"
			_ = msg.NakWithDelay(retryDelay(attempt))
			return
		}
		log.Error().Err(err).Bool("permanent", IsPermanent(err)).Msg("event_parked")
		result = "parked"
		_ = msg.Ack()
		return
	}
	log.Warn().Err(err).Msg("event_retry")
	result = "retry"
	_ = msg.NakWithDelay(retryDelay(attempt))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
