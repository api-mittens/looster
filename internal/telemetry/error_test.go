package telemetry

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestSetupConfigError(t *testing.T) {
	cleanEnv(t)
	t.Setenv("OTEL_LOGS_EXPORTER", "private-invalid-value")
	var stderr bytes.Buffer
	logger, stop, err := Setup(context.Background(), "test", "test", &stderr)
	if err == nil || strings.Contains(err.Error(), "private-invalid-value") {
		t.Fatalf("Unsafe or missing error: %v", err)
	}
	logger.Info("Local work continues")
	if !strings.Contains(stderr.String(), "Local work continues") {
		t.Fatal("Local logger failed")
	}
	if err := stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
