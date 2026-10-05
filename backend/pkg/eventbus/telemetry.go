package eventbus

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const instrumentation = "shopee/backend/pkg/eventbus"

var (
	publishDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "eventbus_publish_duration_seconds",
		Help:    "Time until the broker acknowledged a published event.",
		Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 5},
	}, []string{"type", "result"})
	handleDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "eventbus_handle_duration_seconds",
		Help:    "Time to apply one delivered event, by outcome (ack, retry, parked).",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2, 5, 10, 30},
	}, []string{"consumer", "result"})
	deliveryLag = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "eventbus_delivery_lag_seconds",
		Help:    "Time from the fact (occurred_at) to its consumer starting to apply it: outbox relay, broker and consumer backlog together.",
		Buckets: []float64{.05, .1, .25, .5, 1, 2, 5, 10, 30, 60, 300, 1800},
	}, []string{"consumer"})
)

func tracer() trace.Tracer { return otel.Tracer(instrumentation) }

// startPublishSpan starts the producer span and writes its trace context
// into the message headers, so the consumer's span joins the same trace.
func startPublishSpan(ctx context.Context, env Envelope, header nats.Header) (context.Context, trace.Span) {
	ctx, span := tracer().Start(ctx, "publish "+env.Type,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination.name", Subject(env.Type)),
			attribute.String("messaging.message.id", env.EventID),
		))
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(header))
	return ctx, span
}

// startProcessSpan continues the producer's trace from the headers.
func startProcessSpan(ctx context.Context, durable string, header nats.Header, env Envelope) (context.Context, trace.Span) {
	ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(header))
	return tracer().Start(ctx, "process "+env.Type,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.consumer.group.name", durable),
			attribute.String("messaging.message.id", env.EventID),
		))
}

func observeLag(durable string, occurredAt time.Time) {
	if !occurredAt.IsZero() {
		deliveryLag.WithLabelValues(durable).Observe(time.Since(occurredAt).Seconds())
	}
}
