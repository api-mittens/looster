package telemetry

import (
	"context"
	"errors"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// safeHandler keeps full local records. It sends only a fixed body and selected
// string fields to OTel. It never resolves an application LogValuer for export.
type safeHandler struct {
	local, remote slog.Handler
	attrs         []slog.Attr
	grouped       bool
}

func (h *safeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.local.Enabled(ctx, level) || h.remote.Enabled(ctx, level)
}
func (h *safeHandler) Handle(ctx context.Context, r slog.Record) error {
	var localErr, remoteErr error
	if h.local.Enabled(ctx, r.Level) {
		local := r.Clone()
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
			local.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
		}
		localErr = h.local.Handle(ctx, local)
	}
	if h.remote.Enabled(ctx, r.Level) {
		safe := slog.NewRecord(r.Time, r.Level, safeMessage(r.Message), 0)
		for _, a := range h.attrs {
			safe.AddAttrs(a)
		}
		if !h.grouped {
			r.Attrs(func(a slog.Attr) bool {
				if a, ok := safeAttr(a); ok {
					safe.AddAttrs(a)
				}
				return true
			})
		}
		remoteErr = h.remote.Handle(ctx, safe)
	}
	return errors.Join(localErr, remoteErr)
}
func (h *safeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.local = h.local.WithAttrs(attrs)
	next.attrs = append([]slog.Attr(nil), h.attrs...)
	if !h.grouped {
		for _, a := range attrs {
			if a, ok := safeAttr(a); ok {
				next.attrs = append(next.attrs, a)
			}
		}
	}
	return &next
}
func (h *safeHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := *h
	next.local = h.local.WithGroup(name)
	next.grouped = true
	return &next
}
func safeMessage(message string) string {
	switch message {
	case "Could not load dashboard data", "Could not save attachment or remove object", "worker started", "worker stopped", "scheduled pings dispatched", "Schedule failed", "System event write failed", "Record failure failed", "job interrupted; will resume", "task closed; session stopped", "Worker started", "Worker stopped", "Job attempt started", "Job attempt stopped", "Job failed", "Archive recovery started", "Scheduled pings dispatched", "Claim failed", "Heartbeat failed", "Dashboard API query failed":
		return message
	default:
		return "Application log"
	}
}

func safeAttr(a slog.Attr) (slog.Attr, bool) {
	if (a.Key == "job" || a.Key == "task") && a.Value.Kind() == slog.KindInt64 {
		return a, true
	}
	if a.Value.Kind() != slog.KindString {
		return slog.Attr{}, false
	}
	switch a.Key {
	case "job_id", "task_id", "agent", "session_id", "namespace":
		if len(a.Value.String()) <= 256 {
			return a, true
		}
	case "outcome":
		switch a.Value.String() {
		case "completed", "failed", "timeout", "interrupted", "task_closed":
			return a, true
		}
	case "operation":
		switch a.Value.String() {
		case "schedule", "claim", "heartbeat", "capture", "archive", "complete":
			return a, true
		}
	}
	return slog.Attr{}, false
}
