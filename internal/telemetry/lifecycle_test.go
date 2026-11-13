package telemetry

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func TestSafeLifecycleMessage(t *testing.T) {
	var records []slog.Record
	logger := slog.New(&safeHandler{local: slog.NewTextHandler(io.Discard, nil), remote: captureHandler{&records}})
	logger.InfoContext(context.Background(), "Job attempt stopped", "job", int64(12), "outcome", "completed", "prompt", "private")
	if len(records) != 1 || records[0].Message != "Job attempt stopped" {
		t.Fatal("Missing safe event")
	}
	attrs := map[string]slog.Value{}
	records[0].Attrs(func(a slog.Attr) bool { attrs[a.Key] = a.Value; return true })
	if len(attrs) != 2 || attrs["job"].Int64() != 12 || attrs["outcome"].String() != "completed" {
		t.Fatal("Wrong safe fields")
	}
}
