package config_test

import (
	"testing"

	"shopee/backend/pkg/config"
)

func TestLoadTelemetryDefaults(t *testing.T) {
	t.Setenv("METRICS_ADDR", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "")
	t.Setenv("PPROF_ENABLED", "")
	got, err := config.LoadTelemetry()
	if err != nil {
		t.Fatal(err)
	}
	if got.MetricsAddr != ":9464" || got.OTLPEndpoint != "" || got.SampleRatio != 0.1 || got.Pprof {
		t.Fatalf("unexpected defaults: %+v", got)
	}
}

func TestLoadTelemetryRejectsBadValues(t *testing.T) {
	for name, env := range map[string][2]string{
		"ratio above 1":    {"OTEL_TRACES_SAMPLER_ARG", "1.5"},
		"ratio not number": {"OTEL_TRACES_SAMPLER_ARG", "half"},
		"endpoint no host": {"OTEL_EXPORTER_OTLP_ENDPOINT", "tempo:4318"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("OTEL_TRACES_SAMPLER_ARG", "")
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
			t.Setenv(env[0], env[1])
			if _, err := config.LoadTelemetry(); err == nil {
				t.Fatalf("%s=%s accepted", env[0], env[1])
			}
		})
	}
}
