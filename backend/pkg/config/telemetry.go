package config

import (
	"fmt"
	"net/url"
	"strconv"
)

// Telemetry configures metrics and tracing (pkg/telemetry). None of it is
// secret: the endpoints are internal addresses on the Docker network.
type Telemetry struct {
	// MetricsAddr is the internal listener for /metrics (and /debug/pprof
	// when Pprof is set), separate from the API port so the gateway and the
	// edge proxy never route to it. "off" disables it.
	MetricsAddr string
	// OTLPEndpoint is the OTLP/HTTP base URL traces are exported to (e.g.
	// http://tempo:4318). Empty: spans are not recorded, but trace context
	// is still passed on to the services this one calls.
	OTLPEndpoint string
	// SampleRatio is the share of new traces kept (0..1); a request that
	// arrives with a sampling decision keeps it.
	SampleRatio float64
	Pprof       bool
}

// LoadTelemetry reads METRICS_ADDR, OTEL_EXPORTER_OTLP_ENDPOINT,
// OTEL_TRACES_SAMPLER_ARG and PPROF_ENABLED.
func LoadTelemetry() (Telemetry, error) {
	t := Telemetry{
		MetricsAddr:  getEnv("METRICS_ADDR", ":9464"),
		OTLPEndpoint: getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		Pprof:        getEnv("PPROF_ENABLED", "false") == "true",
	}
	if t.OTLPEndpoint != "" {
		u, err := url.Parse(t.OTLPEndpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Telemetry{}, fmt.Errorf("config: OTEL_EXPORTER_OTLP_ENDPOINT must be an http(s) URL")
		}
	}
	ratio, err := strconv.ParseFloat(getEnv("OTEL_TRACES_SAMPLER_ARG", "0.1"), 64)
	if err != nil || ratio < 0 || ratio > 1 {
		return Telemetry{}, fmt.Errorf("config: OTEL_TRACES_SAMPLER_ARG must be between 0 and 1")
	}
	t.SampleRatio = ratio
	return t, nil
}
