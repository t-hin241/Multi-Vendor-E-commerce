package telemetry

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const instrumentation = "shopee/backend/pkg/telemetry"

// Latency buckets from 5 ms to 30 s (the gateway's upstream timeout).
var latencyBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2, 5, 10, 30}

var (
	serverDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_server_request_duration_seconds",
		Help:    "Time to answer an HTTP request, by route template.",
		Buckets: latencyBuckets,
	}, []string{"route", "method", "code"})
	serverInFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "http_server_requests_in_flight",
		Help: "HTTP requests being answered now.",
	})
)

// Middleware traces and measures each request. It labels by the route
// template (/api/orders/:id), never the raw path, so the number of series
// stays bounded; a request matching no route is "unmatched". Use it right
// after middleware.RequestID and before StructuredLogging, which reads the
// trace id it sets.
func Middleware() gin.HandlerFunc {
	tracer := otel.Tracer(instrumentation)
	return func(c *gin.Context) {
		start := time.Now()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		method := c.Request.Method

		ctx := otel.GetTextMapPropagator().Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))
		ctx, span := tracer.Start(ctx, method+" "+route,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", method),
				attribute.String("http.route", route),
			))
		c.Request = c.Request.WithContext(ctx)
		serverInFlight.Inc()

		c.Next()

		serverInFlight.Dec()
		if named := c.GetString(routeKey); named != "" && route == "unmatched" {
			route = named
			span.SetName(method + " " + route)
			span.SetAttributes(attribute.String("http.route", route))
		}
		status := c.Writer.Status()
		span.SetAttributes(attribute.Int("http.response.status_code", status))
		if status >= 500 {
			span.SetStatus(codes.Error, strconv.Itoa(status))
		}
		span.End()
		serverDuration.WithLabelValues(route, method, codeClass(status)).Observe(time.Since(start).Seconds())
	}
}

const routeKey = "telemetry.route"

// SetRoute names the route of a request Gin did not match itself (the
// gateway dispatches API prefixes from NoRoute). Use a template with a
// bounded number of values, e.g. "/api/orders/*".
func SetRoute(c *gin.Context, route string) {
	c.Set(routeKey, route)
}

// UntrustedEdge drops trace context sent by clients, so every trace starts
// at the public edge with this deployment's sampling: a caller must not be
// able to force every request to be recorded. Use it before Middleware on
// the gateway only; internal services continue the gateway's trace.
func UntrustedEdge() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Header.Del("traceparent")
		c.Request.Header.Del("tracestate")
		c.Request.Header.Del("baggage")
		c.Next()
	}
}

// TraceID is the id of the trace ctx belongs to, or "" when none is
// recorded (tracing off or not sampled).
func TraceID(c *gin.Context) string {
	sc := trace.SpanContextFromContext(c.Request.Context())
	if !sc.IsSampled() {
		return ""
	}
	return sc.TraceID().String()
}

// codeClass groups status codes (2xx, 4xx, ...) for metric labels.
func codeClass(status int) string {
	if status < 100 || status > 599 {
		return "other"
	}
	return strconv.Itoa(status/100) + "xx"
}
