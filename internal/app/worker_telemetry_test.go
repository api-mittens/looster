package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type contextLogHandler struct {
	slog.Handler
	spans *[]trace.SpanContext
}

func (h contextLogHandler) Handle(ctx context.Context, record slog.Record) error {
	*h.spans = append(*h.spans, trace.SpanContextFromContext(ctx))
	return h.Handler.Handle(ctx, record)
}

type telemetryLease struct {
	fakeLease
	captureErr, completeErr error
}

func (l *telemetryLease) Capture(ctx context.Context, o Output) error {
	if l.captureErr != nil {
		return l.captureErr
	}
	return l.fakeLease.Capture(ctx, o)
}
func (l *telemetryLease) Complete(ctx context.Context) error {
	if l.completeErr != nil {
		return l.completeErr
	}
	return l.fakeLease.Complete(ctx)
}

func TestWorkerAttemptTelemetry(t *testing.T) {
	for _, outcome := range []string{"completed", "failed", "timeout", "interrupted", "shutdown", "task_closed", "capture", "upload", "complete", "recovery"} {
		t.Run(outcome, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSpanProcessor(recorder))
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
			secret := "PRIVATE_PROMPT_OUTPUT_PROVIDER_ERROR"
			failure := errors.New(secret)
			l := &telemetryLease{fakeLease: fakeLease{job: Job{ID: 13579, TaskID: 24680, Agent: "private-agent", Prompt: secret}}}
			objects := &fakeObjects{calls: &l.calls}
			harness := &fakeHarness{}
			repo := Repository(fakeRepo{})
			expected := outcome
			switch outcome {
			case "failed":
				harness.err = failure
			case "timeout":
				harness.err = context.DeadlineExceeded
			case "interrupted":
				harness.err = context.Canceled
			case "shutdown":
				expected = "interrupted"
			case "task_closed":
				r := &closingRepo{}
				r.closed.Store(true)
				repo = r
			case "capture":
				l.captureErr = failure
				expected = "failed"
			case "upload":
				objects.err = failure
				expected = "failed"
			case "complete":
				l.completeErr = failure
				expected = "failed"
			case "recovery":
				l.job.State = "archiving"
				l.job.SessionID = "private-session"
				l.job.History = []byte(secret)
				expected = "completed"
			}
			var logs bytes.Buffer
			var logSpans []trace.SpanContext
			service := Service{Repo: repo, Harness: harness, Objects: objects, Log: slog.New(contextLogHandler{slog.NewTextHandler(&logs, nil), &logSpans})}
			parentCtx, parent := provider.Tracer("test").Start(context.Background(), "caller")
			parentCtx, cancel := context.WithCancel(parentCtx)
			defer cancel()
			if outcome == "shutdown" {
				service.Harness = interruptedHarness{cancel: cancel}
			}
			service.process(parentCtx, WorkerConfig{Namespace: "private-namespace"}, l)
			parent.End()
			spans := exporter.GetSpans()
			var rootID string
			var rootFound bool
			for _, span := range spans {
				if span.Name == "looster.job.execute" && (expected == "interrupted" || expected == "task_closed") && span.Status.Code == codes.Error {
					t.Fatal("Expected stop has error status")
				}
				if strings.Contains(fmt.Sprint(span.Attributes, span.Events, span.Status), secret) {
					t.Fatal("Private data in span")
				}
				if span.Name == "looster.job.process" {
					rootFound = true
					rootID = span.SpanContext.SpanID().String()
					if span.Parent.IsValid() {
						t.Fatal("Job has a parent")
					}
					got := attribute.NewSet(span.Attributes...)
					value, _ := got.Value("outcome")
					if value.AsString() != expected {
						t.Fatalf("Outcome = %s", value.AsString())
					}
					wantError := expected == "failed" || expected == "timeout"
					if (span.Status.Code == codes.Error) != wantError {
						t.Fatalf("Status = %v", span.Status)
					}
				}
			}
			if !rootFound {
				t.Fatal("No job span")
			}
			for _, logSpan := range logSpans {
				if !logSpan.IsValid() {
					t.Fatal("Log has no span context")
				}
			}
			if len(recorder.Started()) != len(recorder.Ended()) {
				t.Fatal("Span is open")
			}
			parents := map[string]string{}
			for _, span := range spans {
				parents[span.SpanContext.SpanID().String()] = span.Parent.SpanID().String()
			}
			for _, span := range spans {
				if span.Name != "looster.job.process" && span.Name != "caller" {
					id := span.Parent.SpanID().String()
					for id != rootID && id != "0000000000000000" {
						next, ok := parents[id]
						if !ok {
							t.Fatalf("No parent for %s", span.Name)
						}
						id = next
					}
					if id != rootID {
						t.Fatalf("Bad parent for %s", span.Name)
					}
				}
				if span.EndTime.IsZero() {
					t.Fatal("Span is open")
				}
			}
			if strings.Contains(logs.String(), secret) {
				t.Fatal("Private data in log")
			}
			var data metricdata.ResourceMetrics
			if err := reader.Collect(context.Background(), &data); err != nil {
				t.Fatal(err)
			}
			attempts, active := int64(0), int64(0)
			for _, scope := range data.ScopeMetrics {
				for _, m := range scope.Metrics {
					if strings.Contains(m.Name, "duration") && m.Unit != "s" {
						t.Fatalf("Unit = %s", m.Unit)
					}
					check := func(attrs attribute.Set) {
						for _, a := range attrs.ToSlice() {
							if a.Key != "operation" && a.Key != "outcome" {
								t.Fatalf("Unbounded label: %s", a.Key)
							}
						}
					}
					switch values := m.Data.(type) {
					case metricdata.Sum[int64]:
						for _, point := range values.DataPoints {
							check(point.Attributes)
							if m.Name == "looster.job.attempts" {
								attempts += point.Value
								v, _ := point.Attributes.Value("outcome")
								if v.AsString() != expected {
									t.Fatal("Bad metric outcome")
								}
							}
							if m.Name == "looster.job.active" {
								active += point.Value
							}
						}
					case metricdata.Histogram[float64]:
						for _, point := range values.DataPoints {
							check(point.Attributes)
						}
					}
				}
			}
			if attempts != 1 || active != 0 {
				t.Fatalf("Attempts = %d, active = %d", attempts, active)
			}
		})
	}
}
