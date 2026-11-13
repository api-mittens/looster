package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

func TestShutdownDeadline(t *testing.T) {
	cleanEnv(t)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "60000")
	_, stop, err := Setup(context.Background(), "test", "test", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	_, span := otel.Tracer("test").Start(context.Background(), "test")
	span.End()
	start := time.Now()
	if err := stop(context.Background()); err == nil {
		t.Fatal("Expected shutdown timeout")
	}
	if elapsed := time.Since(start); elapsed < shutdownLimit-time.Second || elapsed > shutdownLimit+time.Second {
		t.Fatalf("Shutdown time = %v", elapsed)
	}
}
