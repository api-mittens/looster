package web

import (
	"context"
	"log/slog"
	"net/http"
)

type requestLoggerKey struct{}

// WithLogger sets the logger for request errors without a process-wide change.
func WithLogger(next http.Handler, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestLoggerKey{}, logger)))
	})
}

func requestLogger(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(requestLoggerKey{}).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}
