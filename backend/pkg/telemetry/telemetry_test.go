package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// recordSpans installs an in-memory tracer provider for one test.
func recordSpans(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	return exporter
}

func newRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware())
	r.GET("/api/orders/:id", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func TestMiddlewareLabelsByRouteTemplate(t *testing.T) {
	spans := recordSpans(t)
	r := newRouter()
	for _, id := range []string{"a1", "b2", "c3"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/orders/"+id+"?token=secret-value", nil))
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/no/such/path", nil))

	if n := testutil.CollectAndCount(serverDuration); n != 2 {
		t.Fatalf("expected 2 series (route template + unmatched), got %d", n)
	}
	got := spans.GetSpans()
	if len(got) != 4 {
		t.Fatalf("expected 4 spans, got %d", len(got))
	}
	if got[0].Name != "GET /api/orders/:id" || got[3].Name != "GET unmatched" {
		t.Fatalf("unexpected span names %q, %q", got[0].Name, got[3].Name)
	}
	for _, s := range got {
		for _, a := range s.Attributes {
			if strings.Contains(a.Value.String(), "secret-value") || strings.Contains(a.Value.String(), "a1") {
				t.Fatalf("span %q records the raw URL: %s=%s", s.Name, a.Key, a.Value.String())
			}
		}
	}
}

func TestClientPassesTraceAndRequestIDOn(t *testing.T) {
	spans := recordSpans(t)
	var gotTraceparent, gotRequestID string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceparent = r.Header.Get("traceparent")
		gotRequestID = r.Header.Get("X-Request-Id")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	ctx, parent := otel.Tracer("test").Start(context.Background(), "caller")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL+"/internal/x?key=secret-value", nil)
	req.Header.Set("X-Request-Id", "req-12345678")
	resp, err := NewHTTPClient(2 * time.Second).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	parent.End()

	traceID := parent.SpanContext().TraceID().String()
	if !strings.Contains(gotTraceparent, traceID) {
		t.Fatalf("traceparent %q does not carry trace %s", gotTraceparent, traceID)
	}
	if gotRequestID != "req-12345678" {
		t.Fatalf("request id not passed on: %q", gotRequestID)
	}
	if req.Header.Get("traceparent") != "" {
		t.Fatal("the caller's request was modified")
	}
	for _, s := range spans.GetSpans() {
		for _, a := range s.Attributes {
			if strings.Contains(a.Value.String(), "secret-value") {
				t.Fatalf("client span records the query string: %s", a.Key)
			}
		}
	}
	if n := testutil.CollectAndCount(clientDuration); n < 1 {
		t.Fatal("no client duration recorded")
	}
}

func TestSetRouteNamesNoRouteDispatch(t *testing.T) {
	spans := recordSpans(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware())
	r.NoRoute(func(c *gin.Context) {
		SetRoute(c, "/api/cart/*")
		c.Status(http.StatusAccepted)
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/cart/items/42", nil))

	if !hasRouteSeries(t, "/api/cart/*") {
		t.Fatal(`no duration series with route="/api/cart/*"`)
	}
	got := spans.GetSpans()
	if len(got) != 1 || got[0].Name != "POST /api/cart/*" {
		t.Fatalf("span not named by the dispatched route: %+v", got)
	}
}

func hasRouteSeries(t *testing.T, route string) bool {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != "http_server_request_duration_seconds" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "route" && l.GetValue() == route {
					return true
				}
			}
		}
	}
	return false
}

func TestCodeClass(t *testing.T) {
	for status, want := range map[int]string{200: "2xx", 204: "2xx", 404: "4xx", 503: "5xx", 0: "other"} {
		if got := codeClass(status); got != want {
			t.Errorf("codeClass(%d) = %q, want %q", status, got, want)
		}
	}
}
