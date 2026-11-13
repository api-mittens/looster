// Package telemetry sets up optional process telemetry.
package telemetry

import (
	"fmt"
	"os"
	"strings"
)

type config struct {
	traces, metrics, logs signal
	propagators           []string
}
type signal struct {
	enabled  bool
	protocol string
}

func readConfig() (config, error) {
	var c config
	disabled := strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED")), "true")
	for _, item := range []struct {
		name   string
		target *signal
	}{{"TRACES", &c.traces}, {"METRICS", &c.metrics}, {"LOGS", &c.logs}} {
		if disabled {
			continue
		}
		selection := strings.TrimSpace(os.Getenv("OTEL_" + item.name + "_EXPORTER"))
		switch selection {
		case "none":
			continue
		case "", "otlp":
		default:
			return c, fmt.Errorf("OTEL_%s_EXPORTER: use otlp or none", item.name)
		}
		item.target.enabled = selection == "otlp" || env("OTEL_EXPORTER_OTLP_"+item.name+"_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT", "") != ""
		if !item.target.enabled {
			continue
		}
		item.target.protocol = env("OTEL_EXPORTER_OTLP_"+item.name+"_PROTOCOL", "OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
		if item.target.protocol != "grpc" && item.target.protocol != "http/protobuf" {
			return c, fmt.Errorf("OTEL_EXPORTER_OTLP_%s_PROTOCOL: use grpc or http/protobuf", item.name)
		}
	}
	for _, p := range strings.Split(env("OTEL_PROPAGATORS", "", "tracecontext,baggage"), ",") {
		p = strings.TrimSpace(p)
		switch p {
		case "tracecontext", "baggage", "none":
			c.propagators = append(c.propagators, p)
		default:
			return c, fmt.Errorf("OTEL_PROPAGATORS: use tracecontext, baggage, or none")
		}
	}
	return c, nil
}

func env(key, fallback, value string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv(fallback)); v != "" {
		return v
	}
	return value
}
