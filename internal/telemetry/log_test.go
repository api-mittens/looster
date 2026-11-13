package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

type captureHandler struct{ records *[]slog.Record }

func (h captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h captureHandler) Handle(_ context.Context, r slog.Record) error {
	*h.records = append(*h.records, r.Clone())
	return nil
}
func (h captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h captureHandler) WithGroup(string) slog.Handler      { return h }

type privateValue struct{}

func (privateValue) LogValue() slog.Value { panic("Private value was resolved") }

func TestSafeHandler(t *testing.T) {
	var records []slog.Record
	h := &safeHandler{local: slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}), remote: captureHandler{&records}}
	logger := slog.New(h)
	logger.With("error", errors.New("private"), "job_id", privateValue{}, "task_id", "task-1").Info("private message", "prompt", "private", "error", privateValue{}, "operation", "invalid", "outcome", "completed", "namespace", strings.Repeat("x", 257))
	logger.With("agent", "agent-1").WithGroup("private").With("task_id", "private").Info("private", "job_id", "private")
	if len(records) != 2 {
		t.Fatal("Missing records")
	}
	for _, r := range records {
		if r.Message != "Application log" {
			t.Fatal("Unsafe body")
		}
		r.Attrs(func(a slog.Attr) bool {
			if strings.Contains(a.Value.String(), "private") {
				t.Fatal("Unsafe attribute")
			}
			return true
		})
	}
	attrs := map[string]string{}
	records[0].Attrs(func(a slog.Attr) bool { attrs[a.Key] = a.Value.String(); return true })
	if len(attrs) != 2 || attrs["task_id"] != "task-1" || attrs["outcome"] != "completed" {
		t.Fatalf("Wrong safe fields: %v", attrs)
	}
}
