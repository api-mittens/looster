package telemetry

import (
	"os"
	"strings"
	"testing"
)

func cleanEnv(t *testing.T) {
	t.Helper()
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(key, "OTEL_") {
			t.Setenv(key, "")
		}
	}
}

func TestConfig(t *testing.T) {
	tests := []struct {
		name     string
		vars     map[string]string
		want     [3]bool
		protocol string
		bad      bool
	}{
		{name: "default"},
		{name: "resource only", vars: map[string]string{"OTEL_SERVICE_NAME": "test", "OTEL_RESOURCE_ATTRIBUTES": "a=b"}},
		{name: "traces", vars: map[string]string{"OTEL_TRACES_EXPORTER": "otlp"}, want: [3]bool{true, false, false}},
		{name: "metrics", vars: map[string]string{"OTEL_METRICS_EXPORTER": "otlp"}, want: [3]bool{false, true, false}},
		{name: "logs", vars: map[string]string{"OTEL_LOGS_EXPORTER": "otlp"}, want: [3]bool{false, false, true}},
		{name: "shared", vars: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318"}, want: [3]bool{true, true, true}},
		{name: "signal", vars: map[string]string{"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://localhost:4318/v1/logs"}, want: [3]bool{false, false, true}},
		{name: "none wins", vars: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318", "OTEL_LOGS_EXPORTER": "none"}, want: [3]bool{true, true, false}},
		{name: "disabled", vars: map[string]string{"OTEL_SDK_DISABLED": "true", "OTEL_TRACES_EXPORTER": "invalid"}},
		{name: "empty", vars: map[string]string{"OTEL_TRACES_EXPORTER": " ", "OTEL_EXPORTER_OTLP_ENDPOINT": ""}},
		{name: "http", vars: map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"}, want: [3]bool{true, false, false}, protocol: "http/protobuf"},
		{name: "protocol precedence", vars: map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_PROTOCOL": "invalid", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL": "grpc"}, want: [3]bool{true, false, false}, protocol: "grpc"},
		{name: "bad exporter", vars: map[string]string{"OTEL_LOGS_EXPORTER": "console"}, bad: true},
		{name: "bad protocol", vars: map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_PROTOCOL": "http/json"}, bad: true},
		{name: "bad propagator", vars: map[string]string{"OTEL_PROPAGATORS": "b3"}, bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanEnv(t)
			for k, v := range tt.vars {
				t.Setenv(k, v)
			}
			c, err := readConfig()
			if (err != nil) != tt.bad {
				t.Fatalf("error = %v", err)
			}
			if tt.bad {
				return
			}
			got := [3]bool{c.traces.enabled, c.metrics.enabled, c.logs.enabled}
			if got != tt.want {
				t.Fatalf("signals = %v, want %v", got, tt.want)
			}
			if tt.protocol != "" && c.traces.protocol != tt.protocol {
				t.Fatalf("protocol = %s", c.traces.protocol)
			}
		})
	}
}
