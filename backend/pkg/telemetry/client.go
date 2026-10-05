package telemetry

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var clientDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "http_client_request_duration_seconds",
	Help:    "Time for a call to another service or provider, by peer host.",
	Buckets: latencyBuckets,
}, []string{"peer", "method", "code"})

// sharedTransport is the connection pool for every outbound call of the
// process. http.DefaultTransport keeps only 2 idle connections per host,
// so concurrent calls to the same service open and close a TCP connection
// each time; this keeps enough of them warm.
var sharedTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 256
	t.MaxIdleConnsPerHost = 32
	t.IdleConnTimeout = 90 * time.Second
	return t
}()

// NewHTTPClient returns a client with the given overall timeout that
// shares the process connection pool, passes the trace context on and
// measures each call.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: Transport(sharedTransport)}
}

// Transport wraps next with tracing and metrics (the gateway wraps its own
// proxy transport). The span records method, peer host and status only,
// not the URL: query strings can carry tokens.
func Transport(next http.RoundTripper) http.RoundTripper {
	return roundTripper{next: next, tracer: otel.Tracer(instrumentation)}
}

type roundTripper struct {
	next   http.RoundTripper
	tracer trace.Tracer
}

func (rt roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	peer := req.URL.Host
	ctx, span := rt.tracer.Start(req.Context(), req.Method+" "+peer,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("http.request.method", req.Method),
			attribute.String("server.address", peer),
		))
	defer span.End()

	// RoundTrippers must not modify the caller's request.
	out := req.Clone(ctx)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(out.Header))

	resp, err := rt.next.RoundTrip(out)
	code := "error"
	if err != nil {
		span.SetStatus(codes.Error, "transport error")
	} else {
		code = codeClass(resp.StatusCode)
		span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
		if resp.StatusCode >= 500 {
			span.SetStatus(codes.Error, resp.Status)
		}
	}
	clientDuration.WithLabelValues(peer, req.Method, code).Observe(time.Since(start).Seconds())
	return resp, err
}
