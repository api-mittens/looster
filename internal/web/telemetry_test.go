package web

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type streamingTestWriter struct{ *httptest.ResponseRecorder }

func (*streamingTestWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("No connection")
}
func (*streamingTestWriter) Push(string, *http.PushOptions) error { return http.ErrNotSupported }
func (*streamingTestWriter) CloseNotify() <-chan bool             { return make(chan bool) }
func (w *streamingTestWriter) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(w.ResponseRecorder, r)
}

func TestHTTPTelemetryWriterInterfaces(t *testing.T) {
	telemetryTestProviders(t)
	routes := http.NewServeMux()
	routes.HandleFunc("GET /stream", func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := w.(http.Hijacker); !ok {
			t.Error("Hijacker is missing")
		}
		if _, ok := w.(http.Pusher); !ok {
			t.Error("Pusher is missing")
		}
		if _, ok := w.(http.CloseNotifier); !ok {
			t.Error("CloseNotifier is missing")
		}
		if _, ok := w.(io.ReaderFrom); !ok {
			t.Error("ReaderFrom is missing")
		}
		w.(http.Flusher).Flush()
		_, _ = w.(io.ReaderFrom).ReadFrom(strings.NewReader("stream"))
	})
	w := &streamingTestWriter{httptest.NewRecorder()}
	instrumentHTTP(routes, routes).ServeHTTP(w, httptest.NewRequest("GET", "/stream", nil))
	if !w.Flushed || w.Body.String() != "stream" {
		t.Fatal("Stream changed")
	}
}

type requestLogHandler struct {
	slog.Handler
	span trace.SpanContext
}

func (h *requestLogHandler) Handle(ctx context.Context, record slog.Record) error {
	h.span = trace.SpanContextFromContext(ctx)
	return h.Handler.Handle(ctx, record)
}

func TestHTTPTelemetrySafeErrorLog(t *testing.T) {
	recorder, _ := telemetryTestProviders(t)
	var output bytes.Buffer
	logs := &requestLogHandler{Handler: slog.NewTextHandler(&output, nil)}
	WithLogger(Handler(&testRepo{fail: true}), slog.New(logs)).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/board", nil))
	if !logs.span.IsValid() || logs.span.SpanID() != recorder.Ended()[0].SpanContext().SpanID() {
		t.Fatal("Log request context is missing")
	}
	if strings.Contains(output.String(), "private") || !strings.Contains(output.String(), "error_class=internal") {
		t.Fatalf("Unsafe error log: %s", output.String())
	}
}

func telemetryTestProviders(t *testing.T) (*tracetest.SpanRecorder, *sdkmetric.ManualReader) {
	t.Helper()
	oldTrace, oldMeter, oldPropagation := otel.GetTracerProvider(), otel.GetMeterProvider(), otel.GetTextMapPropagator()
	recorder := tracetest.NewSpanRecorder()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetTracerProvider(tracer)
	otel.SetMeterProvider(meter)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(oldTrace)
		otel.SetMeterProvider(oldMeter)
		otel.SetTextMapPropagator(oldPropagation)
		_ = tracer.Shutdown(context.Background())
		_ = meter.Shutdown(context.Background())
	})
	return recorder, reader
}

func TestHTTPTelemetryRoutesAndPrivacy(t *testing.T) {
	recorder, reader := telemetryTestProviders(t)
	handler := Handler(&testRepo{}, testNamespaces{repos: map[string]Repository{"private-namespace": &testRepo{}}})
	tests := []struct {
		path, route string
		status      int
	}{
		{"/api/tasks/TSK-1?namespace=private-namespace&secret=private-query", "/api/tasks/{id}", 200},
		{"/api/tasks/TSK-99999?namespace=private-namespace", "/api/tasks/{id}", 404},
		{"/api/tasks/TSK-1?namespace=private-missing", "/api/tasks/{id}", 404},
		{"/api/namespaces", "/api/namespaces", 200},
		{"/api/private-unknown?secret=private-query", unmatchedHTTPRoute, 404},
		{"/private-file", unmatchedHTTPRoute, 404},
	}
	for _, tt := range tests {
		request := httptest.NewRequest("GET", "http://private-host"+tt.path, nil)
		request.Header.Set("User-Agent", "private-agent")
		request.Header.Set("Authorization", "private-token")
		request.Header.Set("X-Forwarded-For", "private-forward")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != tt.status {
			t.Fatalf("Status = %d, want %d", response.Code, tt.status)
		}
		spans := recorder.Ended()
		span := spans[len(spans)-1]
		if span.Name() != "GET "+tt.route {
			t.Fatalf("Span name = %q", span.Name())
		}
		if got := attributeValue(span.Attributes(), "http.route"); got != tt.route {
			t.Fatalf("Route = %q", got)
		}
		assertNoPrivateTelemetry(t, fmt.Sprint(span.Attributes()))
	}
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	assertNoPrivateTelemetry(t, fmt.Sprint(metrics))
	found := false
	for _, scope := range metrics.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == "http.server.request.duration" {
				found = true
				if m.Unit != "s" {
					t.Fatalf("Duration unit = %q", m.Unit)
				}
				points := m.Data.(metricdata.Histogram[float64]).DataPoints
				for _, point := range points {
					for _, attr := range point.Attributes.ToSlice() {
						switch string(attr.Key) {
						case "http.request.method", "http.route", "http.response.status_code", "network.protocol.name", "network.protocol.version", "url.scheme", "server.address", "server.port", "error.type":
						default:
							t.Fatalf("Metric label is not bounded: %s", attr.Key)
						}
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("HTTP duration metric is missing")
	}
}

func attributeValue(attrs []attribute.KeyValue, key string) string {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a.Value.AsString()
		}
	}
	return ""
}
func assertNoPrivateTelemetry(t *testing.T, value string) {
	t.Helper()
	if strings.Contains(value, "private-") || strings.Contains(value, "TSK-99999") {
		t.Fatalf("Private value in telemetry: %s", value)
	}
}

func TestHTTPTelemetryPropagationAndStreaming(t *testing.T) {
	recorder, _ := telemetryTestProviders(t)
	routes := http.NewServeMux()
	routes.HandleFunc("GET /stream/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "private-id" || r.URL.Query().Get("secret") != "private-query" || r.Header.Get("X-Private") != "private-header" {
			t.Error("Application request changed")
		}
		if !trace.SpanContextFromContext(r.Context()).IsValid() {
			t.Error("Request span is missing")
		}
		if _, ok := w.(http.Hijacker); ok {
			t.Error("Wrapper added Hijacker")
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("Flusher is missing")
		}
		_, _ = w.Write([]byte("first"))
		flusher.Flush()
		_, _ = w.Write([]byte("second"))
	})
	handler := instrumentHTTP(routes, routes)
	request := httptest.NewRequest("GET", "/stream/private-id?secret=private-query", nil)
	request.Header.Set("X-Private", "private-header")
	request.Header.Set("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if !response.Flushed || response.Body.String() != "firstsecond" {
		t.Fatal("Stream changed")
	}
	span := recorder.Ended()[0]
	if span.Parent().SpanID().String() != "0123456789abcdef" || !span.Parent().IsRemote() {
		t.Fatalf("Parent = %v", span.Parent())
	}
	assertNoPrivateTelemetry(t, fmt.Sprint(span.Attributes()))
}

func TestHTTPTelemetryErrorStatusAndBoundedMethod(t *testing.T) {
	recorder, reader := telemetryTestProviders(t)
	handler := Handler(&testRepo{fail: true})
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/board", nil))
	if recorder.Ended()[0].Status().Code != codes.Error {
		t.Fatal("Error status is missing")
	}
	for i := 0; i < 20; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(fmt.Sprintf("PRIVATE%d", i), fmt.Sprintf("/api/private-%d", i), nil))
	}
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	for _, scope := range metrics.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name == "http.server.request.duration" && len(metric.Data.(metricdata.Histogram[float64]).DataPoints) > 2 {
				t.Fatal("Custom methods added metric series")
			}
		}
	}
	for _, span := range recorder.Ended()[1:] {
		if span.Name() != "_OTHER "+unmatchedHTTPRoute {
			t.Fatalf("Unbounded name: %s", span.Name())
		}
		assertNoPrivateTelemetry(t, fmt.Sprint(span.Attributes()))
	}
}
