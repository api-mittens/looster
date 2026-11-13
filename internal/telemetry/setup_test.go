package telemetry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	collectorlog "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type receiver struct {
	mu      sync.Mutex
	traces  []*collectortrace.ExportTraceServiceRequest
	metrics []*collectormetric.ExportMetricsServiceRequest
	logs    []*collectorlog.ExportLogsServiceRequest
}
type traceReceiver struct {
	collectortrace.UnimplementedTraceServiceServer
	r *receiver
}

func (s traceReceiver) Export(_ context.Context, r *collectortrace.ExportTraceServiceRequest) (*collectortrace.ExportTraceServiceResponse, error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	s.r.traces = append(s.r.traces, r)
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

type metricReceiver struct {
	collectormetric.UnimplementedMetricsServiceServer
	r *receiver
}

func (s metricReceiver) Export(_ context.Context, r *collectormetric.ExportMetricsServiceRequest) (*collectormetric.ExportMetricsServiceResponse, error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	s.r.metrics = append(s.r.metrics, r)
	return &collectormetric.ExportMetricsServiceResponse{}, nil
}

type logReceiver struct {
	collectorlog.UnimplementedLogsServiceServer
	r *receiver
}

func (s logReceiver) Export(_ context.Context, r *collectorlog.ExportLogsServiceRequest) (*collectorlog.ExportLogsServiceResponse, error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	s.r.logs = append(s.r.logs, r)
	return &collectorlog.ExportLogsServiceResponse{}, nil
}

func (r *receiver) serveHTTP(w http.ResponseWriter, req *http.Request) {
	data, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "Read failed", 400)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var msg proto.Message
	switch req.URL.Path {
	case "/v1/traces":
		v := &collectortrace.ExportTraceServiceRequest{}
		msg = v
		r.traces = append(r.traces, v)
	case "/v1/metrics":
		v := &collectormetric.ExportMetricsServiceRequest{}
		msg = v
		r.metrics = append(r.metrics, v)
	case "/v1/logs":
		v := &collectorlog.ExportLogsServiceRequest{}
		msg = v
		r.logs = append(r.logs, v)
	default:
		http.NotFound(w, req)
		return
	}
	if err := proto.Unmarshal(data, msg); err != nil {
		http.Error(w, "Decode failed", 400)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
}

func TestSDKExport(t *testing.T) {
	for _, protocol := range []string{"grpc", "http/protobuf"} {
		t.Run(protocol, func(t *testing.T) {
			cleanEnv(t)
			r := &receiver{}
			var endpoint string
			if protocol == "grpc" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				server := grpc.NewServer()
				collectortrace.RegisterTraceServiceServer(server, traceReceiver{r: r})
				collectormetric.RegisterMetricsServiceServer(server, metricReceiver{r: r})
				collectorlog.RegisterLogsServiceServer(server, logReceiver{r: r})
				go func() { _ = server.Serve(listener) }()
				t.Cleanup(server.Stop)
				endpoint = "http://" + listener.Addr().String()
			} else {
				server := httptest.NewServer(http.HandlerFunc(r.serveHTTP))
				t.Cleanup(server.Close)
				endpoint = server.URL
			}
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint)
			t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", protocol)
			t.Setenv("OTEL_SERVICE_NAME", "override")
			t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=wrong,service.version=wrong,test.attribute=present")
			t.Setenv("OTEL_TRACES_SAMPLER", "always_on")
			var stderr bytes.Buffer
			logger, stop, err := Setup(context.Background(), "default", "1.2.3", &stderr)
			if err != nil {
				t.Fatal(err)
			}
			otel.Handle(errors.New("private-sdk-error"))
			ctx, span := otel.Tracer("test").Start(context.Background(), "test.root")
			logger.With("job_id", "job-1", "error", errors.New("private-value")).ErrorContext(ctx, "private-message", "prompt", "private-prompt", "operation", "archive")
			span.End()
			counter, err := otel.Meter("test").Int64Counter("test.count")
			if err != nil {
				t.Fatal(err)
			}
			counter.Add(ctx, 1)
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if err := stop(canceled); err != nil {
				t.Fatal(err)
			}
			if err := stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			if len(r.traces) == 0 || len(r.metrics) == 0 || len(r.logs) == 0 {
				t.Fatalf("Missing signals: %d %d %d", len(r.traces), len(r.metrics), len(r.logs))
			}
			resource := r.traces[0].ResourceSpans[0].Resource
			attrs := map[string]string{}
			for _, a := range resource.Attributes {
				attrs[a.Key] = a.Value.GetStringValue()
			}
			if attrs["service.name"] != "override" || attrs["service.version"] != "1.2.3" || attrs["test.attribute"] != "present" {
				t.Fatalf("Resource: %v", attrs)
			}
			if len(r.logs) != 1 || len(r.logs[0].ResourceLogs[0].ScopeLogs[0].LogRecords) != 1 {
				t.Fatal("SDK errors entered the log exporter")
			}
			record := r.logs[0].ResourceLogs[0].ScopeLogs[0].LogRecords[0]
			traceID := span.SpanContext().TraceID()
			if !bytes.Equal(record.TraceId, traceID[:]) {
				t.Fatal("Trace ID changed")
			}
			if len(record.TraceId) != 16 || len(record.SpanId) != 8 {
				t.Fatal("Missing trace context")
			}
			wire := r.logs[0].String()
			if strings.Contains(wire, "private") || !strings.Contains(wire, "job-1") || !strings.Contains(wire, "archive") {
				t.Fatalf("Unsafe or missing log fields: %s", wire)
			}
			if !strings.Contains(stderr.String(), "private-value") || !strings.Contains(stderr.String(), "private-message") {
				t.Fatal("Local log lost fields")
			}
		})
	}
}

func TestDisabled(t *testing.T) {
	cleanEnv(t)
	var stderr bytes.Buffer
	logger, stop, err := Setup(context.Background(), "test", "test", &stderr)
	if err != nil {
		t.Fatal(err)
	}
	_, span := otel.Tracer("test").Start(context.Background(), "test")
	defer span.End()
	if span.IsRecording() {
		t.Fatal("Disabled span records data")
	}
	logger.Info("Local log")
	if !strings.Contains(stderr.String(), "Local log") {
		t.Fatal("Local log missing")
	}
	if err := stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSamplerAndPropagation(t *testing.T) {
	cleanEnv(t)
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	t.Setenv("OTEL_TRACES_SAMPLER", "always_off")
	_, stop, err := Setup(context.Background(), "test", "test", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer stop(context.Background())
	_, span := otel.Tracer("test").Start(context.Background(), "test")
	defer span.End()
	if span.IsRecording() {
		t.Fatal("Sampler did not stop recording")
	}
	carrier := propagation.MapCarrier{"traceparent": "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"}
	ctx := otel.GetTextMapPropagator().Extract(context.Background(), carrier)
	out := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, out)
	if out["traceparent"] != carrier["traceparent"] {
		t.Fatal("Trace context changed")
	}
}

func TestExportFailure(t *testing.T) {
	cleanEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "private-provider-error", http.StatusBadRequest)
	}))
	defer server.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	var stderr bytes.Buffer
	logger, stop, err := Setup(context.Background(), "test", "test", &stderr)
	if err != nil {
		t.Fatal(err)
	}
	logger.Error("Work failed", "error", errors.New("private-error"))
	_, span := otel.Tracer("test").Start(context.Background(), "test")
	span.End()
	start := time.Now()
	_ = stop(context.Background())
	if time.Since(start) > shutdownLimit+time.Second {
		t.Fatal("Shutdown limit exceeded")
	}
	if strings.Contains(stderr.String(), "private-provider-error") {
		t.Fatal("SDK error text reached stderr")
	}
}
