// Package telemetry gives every service the same metrics and tracing:
// Prometheus metrics on an internal listener (never the API port), and
// OpenTelemetry traces exported over OTLP/HTTP. Spans carry routes, methods,
// status codes and peer hosts only: never query strings, headers, bodies or
// SQL arguments, so no token or personal data reaches the trace store.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// Options mirrors config.Telemetry (convert with telemetry.Options(cfg)); it is
// declared here so this package does not depend on pkg/config.
type Options struct {
	MetricsAddr  string
	OTLPEndpoint string
	SampleRatio  float64
	Pprof        bool
}

// Telemetry is one process's metrics listener and tracer provider.
type Telemetry struct {
	tp  *sdktrace.TracerProvider
	srv *http.Server
	log zerolog.Logger
}

// Setup installs the global tracer provider and W3C trace-context
// propagation, and starts the internal metrics listener. Call it first in
// main; Shutdown flushes the spans still buffered.
func Setup(service string, cfg Options, log zerolog.Logger) (*Telemetry, error) {
	// Always propagate, even without an exporter: a service in the middle
	// of a call chain must pass the caller's trace on.
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t := &Telemetry{log: log}

	if cfg.OTLPEndpoint != "" {
		exporter, err := otlptracehttp.New(context.Background(), otlptracehttp.WithEndpointURL(cfg.OTLPEndpoint+"/v1/traces"))
		if err != nil {
			return nil, fmt.Errorf("telemetry: otlp exporter: %w", err)
		}
		res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(service)))
		if err != nil {
			return nil, fmt.Errorf("telemetry: resource: %w", err)
		}
		t.tp = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exporter),
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
		)
		otel.SetTracerProvider(t.tp)
	}

	if cfg.MetricsAddr != "off" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		if cfg.Pprof {
			mux.HandleFunc("/debug/pprof/", pprof.Index)
			mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
			mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
			mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
			mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		}
		ln, err := net.Listen("tcp", cfg.MetricsAddr)
		if err != nil {
			return nil, fmt.Errorf("telemetry: metrics listener: %w", err)
		}
		// A CPU profile takes 30s by default; nothing else here is slow.
		t.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 2 * time.Minute}
		go func() {
			if err := t.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error().Err(err).Msg("metrics_listener_failed")
			}
		}()
		log.Info().Str("addr", cfg.MetricsAddr).Bool("pprof", cfg.Pprof).Bool("tracing", t.tp != nil).Msg("telemetry_started")
	}
	return t, nil
}

// Close stops the metrics listener and flushes buffered spans, waiting at
// most 5s (an unreachable trace store must not hold up the exit). Defer it
// right after Setup: it then runs after the HTTP server and workers stop.
func (t *Telemetry) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.Shutdown(ctx)
}

// Shutdown stops the metrics listener and flushes buffered spans.
func (t *Telemetry) Shutdown(ctx context.Context) {
	if t.srv != nil {
		_ = t.srv.Shutdown(ctx)
	}
	if t.tp != nil {
		if err := t.tp.Shutdown(ctx); err != nil {
			t.log.Warn().Err(err).Msg("trace_flush_failed")
		}
	}
}
