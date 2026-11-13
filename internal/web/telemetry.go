package web

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/api-mittens/looster/internal/app"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const unmatchedHTTPRoute = "/unmatched"

type originalRequestKey struct{}

// instrumentHTTP gives the SDK a safe request copy. The application keeps the
// original URL and headers. The SDK uses the global providers.
func instrumentHTTP(next http.Handler, routes *http.ServeMux) http.Handler {
	active, _ := otel.Meter("github.com/api-mittens/looster/internal/web").Int64UpDownCounter("http.server.active_requests", metric.WithUnit("{request}"))
	instrumented := otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, safe *http.Request) {
		original := safe.Context().Value(originalRequestKey{}).(*http.Request)
		request := original.WithContext(safe.Context())
		request.Body = safe.Body
		route := attribute.String("http.route", safe.URL.Path)
		trace.SpanFromContext(safe.Context()).SetAttributes(route)
		labeler, _ := otelhttp.LabelerFromContext(safe.Context())
		labeler.Add(route)
		next.ServeHTTP(w, request)
	}), "HTTP", otelhttp.WithTracerProvider(otel.GetTracerProvider()), otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
		return r.Method + " " + r.URL.Path
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opts := metric.WithAttributes(attribute.String("http.request.method", boundedHTTPMethod(r.Method)))
		active.Add(r.Context(), 1, opts)
		defer active.Add(r.Context(), -1, opts)
		route := unmatchedHTTPRoute
		if r.URL.Path == "/api/namespaces" && r.Method == http.MethodGet {
			route = "/api/namespaces"
		} else {
			_, pattern := routes.Handler(r)
			if _, path, found := strings.Cut(pattern, " "); found && path != "/api/" {
				route = path
				if path == "/" && r.URL.Path != "/" {
					if info, err := fs.Stat(assets, "static"+r.URL.Path); err != nil || info.IsDir() {
						route = unmatchedHTTPRoute
					}
				}
			}
		}
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx = context.WithValue(ctx, originalRequestKey{}, r)
		safe := r.WithContext(ctx)
		safe.URL = &url.URL{Path: route}
		safe.RequestURI = route
		safe.Host = ""
		safe.RemoteAddr = ""
		safe.Header = make(http.Header)
		safe.Pattern = ""
		safe.Method = boundedHTTPMethod(r.Method)
		instrumented.ServeHTTP(w, safe)
	})
}

func boundedHTTPMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace, http.MethodPatch:
		return method
	default:
		return "_OTHER"
	}
}

func dashboardErrorClass(err error) string {
	switch {
	case errors.Is(err, app.ErrNotFound):
		return "not_found"
	case errors.Is(err, app.ErrForbidden):
		return "forbidden"
	case errors.Is(err, app.ErrColumnPaginationUnavailable):
		return "unavailable"
	default:
		return "internal"
	}
}
