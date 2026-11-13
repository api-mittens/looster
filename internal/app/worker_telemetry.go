package app

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const workerScope = "github.com/api-mittens/looster/internal/app/worker"

// Instruments use only fixed operation and outcome values.
type workerTelemetry struct {
	attempts          metric.Int64Counter
	duration          metric.Float64Histogram
	active            metric.Int64UpDownCounter
	capacity          metric.Int64Gauge
	operationDuration metric.Float64Histogram
	operationErrors   metric.Int64Counter
	dispatched        metric.Int64Counter
}

func newWorkerTelemetry() workerTelemetry {
	m := otel.Meter(workerScope)
	attempts, _ := m.Int64Counter("looster.job.attempts")
	duration, _ := m.Float64Histogram("looster.job.duration", metric.WithUnit("s"))
	active, _ := m.Int64UpDownCounter("looster.job.active")
	capacity, _ := m.Int64Gauge("looster.worker.capacity")
	operationDuration, _ := m.Float64Histogram("looster.operation.duration", metric.WithUnit("s"))
	operationErrors, _ := m.Int64Counter("looster.operation.errors")
	dispatched, _ := m.Int64Counter("looster.scheduler.dispatched")
	return workerTelemetry{attempts, duration, active, capacity, operationDuration, operationErrors, dispatched}
}

func workerOutcome(err error) string {
	switch {
	case err == nil:
		return "completed"
	case errors.Is(err, ErrTaskClosed):
		return "task_closed"
	case errors.Is(err, context.Canceled):
		return "interrupted"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "failed"
	}
}

func workerSpanEnd(span trace.Span, err error) {
	outcome := workerOutcome(err)
	span.SetAttributes(attribute.String("outcome", outcome))
	if outcome == "failed" || outcome == "timeout" {
		// Do not export provider error text. It can contain private data.
		span.SetStatus(codes.Error, "Operation failed")
		span.SetAttributes(attribute.String("error.type", outcome))
	}
	span.End()
}

func (t workerTelemetry) operation(ctx context.Context, name string, start time.Time, err error, useful bool) {
	attrs := metric.WithAttributes(attribute.String("operation", name), attribute.String("outcome", workerOutcome(err)))
	t.operationDuration.Record(ctx, time.Since(start).Seconds(), attrs)
	if err != nil {
		t.operationErrors.Add(ctx, 1, attrs)
	}
	if useful || err != nil {
		_, span := otel.Tracer(workerScope).Start(ctx, "looster.worker."+name, trace.WithTimestamp(start))
		workerSpanEnd(span, err)
	}
}

func (s *Service) heartbeat(ctx context.Context, c WorkerConfig, settings WorkerSettings) error {
	start := time.Now()
	err := s.Queue.Heartbeat(ctx, c.ID, c.Agents, settings)
	newWorkerTelemetry().operation(ctx, "heartbeat", start, err, false)
	return err
}

func (s *Service) claim(ctx context.Context, c WorkerConfig) (Lease, error) {
	start := time.Now()
	lease, err := s.Queue.Claim(ctx, c.Agents)
	newWorkerTelemetry().operation(ctx, "claim", start, err, lease != nil)
	return lease, err
}
