package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type telemetryQueue struct {
	Queue
	count int
	err   error
}

func (q telemetryQueue) Schedule(context.Context, []string) (int, error) { return q.count, q.err }
func (q telemetryQueue) Claim(context.Context, []string) (Lease, error)  { return nil, q.err }
func (q telemetryQueue) Heartbeat(context.Context, string, []string, WorkerSettings) error {
	return q.err
}
func (q telemetryQueue) RecordSystemEvent(context.Context, string, string) error { return nil }

func TestWorkerPollTelemetry(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	oldTrace, oldMeter := otel.GetTracerProvider(), otel.GetMeterProvider()
	otel.SetTracerProvider(provider)
	otel.SetMeterProvider(meter)
	defer func() {
		otel.SetTracerProvider(oldTrace)
		otel.SetMeterProvider(oldMeter)
		_ = provider.Shutdown(context.Background())
		_ = meter.Shutdown(context.Background())
	}()
	ctx := context.Background()
	service := Service{Queue: telemetryQueue{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	_, _ = service.schedule(ctx, WorkerConfig{})
	_, _ = service.claim(ctx, WorkerConfig{})
	_ = service.heartbeat(ctx, WorkerConfig{}, WorkerSettings{})
	if len(exporter.GetSpans()) != 0 {
		t.Fatal("Empty polls made spans")
	}
	service.Queue = telemetryQueue{count: 3}
	_, _ = service.schedule(ctx, WorkerConfig{})
	service.Queue = telemetryQueue{count: 99, err: errors.New("private database URL")}
	_, _ = service.schedule(ctx, WorkerConfig{})
	_, _ = service.claim(ctx, WorkerConfig{})
	_ = service.heartbeat(ctx, WorkerConfig{}, WorkerSettings{})
	if len(exporter.GetSpans()) != 4 {
		t.Fatalf("Spans = %d", len(exporter.GetSpans()))
	}
	var data metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &data); err != nil {
		t.Fatal(err)
	}
	dispatched, failures, calls := int64(0), int64(0), uint64(0)
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			switch values := m.Data.(type) {
			case metricdata.Sum[int64]:
				for _, point := range values.DataPoints {
					if m.Name == "looster.scheduler.dispatched" {
						dispatched += point.Value
					}
					if m.Name == "looster.operation.errors" {
						failures += point.Value
					}
				}
			case metricdata.Histogram[float64]:
				for _, point := range values.DataPoints {
					if m.Name == "looster.operation.duration" {
						calls += point.Count
					}
				}
			}
		}
	}
	if dispatched != 3 || failures != 3 || calls != 7 {
		t.Fatalf("Dispatch = %d, failures = %d, calls = %d", dispatched, failures, calls)
	}
}
