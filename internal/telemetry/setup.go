package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

const shutdownLimit = 5 * time.Second

// Setup installs process providers. Call it once, before work starts.
// Shutdown uses a fresh context with a five-second limit. Stop work first.
// The returned logger always writes complete records to stderr.
func Setup(ctx context.Context, service, version string, stderr io.Writer) (*slog.Logger, func(context.Context) error, error) {
	if stderr == nil {
		stderr = io.Discard
	}
	local := slog.NewTextHandler(stderr, nil)
	logger := slog.New(local)
	var stops []func(context.Context) error
	var once sync.Once
	var stopErr error
	shutdown := func(context.Context) error {
		once.Do(func() {
			fresh, cancel := context.WithTimeout(context.Background(), shutdownLimit)
			defer cancel()
			for i := len(stops) - 1; i >= 0; i-- {
				stopErr = errors.Join(stopErr, stops[i](fresh))
			}
		})
		return stopErr
	}
	c, err := readConfig()
	if err != nil {
		return logger, shutdown, err
	}
	// SDK errors go to stderr only. Do not include provider error text.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {
		_ = local.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelWarn, "Telemetry export failed", 0))
	}))
	var props []propagation.TextMapPropagator
	for _, p := range c.propagators {
		switch p {
		case "tracecontext":
			props = append(props, propagation.TraceContext{})
		case "baggage":
			props = append(props, propagation.Baggage{})
		}
	}
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(props...))
	tp := tracenoop.NewTracerProvider()
	mp := metricnoop.NewMeterProvider()
	if !c.traces.enabled && !c.metrics.enabled && !c.logs.enabled {
		otel.SetTracerProvider(tp)
		otel.SetMeterProvider(mp)
		return logger, shutdown, nil
	}
	res, err := resource.New(ctx, resource.WithAttributes(attribute.String("service.name", service)), resource.WithFromEnv(), resource.WithAttributes(attribute.String("service.version", version)))
	if err != nil {
		return logger, shutdown, errors.New("telemetry resource setup failed")
	}
	fail := func() (*slog.Logger, func(context.Context) error, error) {
		_ = shutdown(context.Background())
		return logger, shutdown, errors.New("telemetry exporter setup failed: check OTEL settings")
	}
	var traces *sdktrace.TracerProvider
	var metrics *sdkmetric.MeterProvider
	if c.traces.enabled {
		var exp sdktrace.SpanExporter
		if c.traces.protocol == "grpc" {
			exp, err = otlptracegrpc.New(ctx)
		} else {
			exp, err = otlptracehttp.New(ctx)
		}
		if err != nil {
			return fail()
		}
		traces = sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithBatcher(exp))
		stops = append(stops, traces.Shutdown)
	}
	if c.metrics.enabled {
		var exp sdkmetric.Exporter
		if c.metrics.protocol == "grpc" {
			exp, err = otlpmetricgrpc.New(ctx)
		} else {
			exp, err = otlpmetrichttp.New(ctx)
		}
		if err != nil {
			return fail()
		}
		metrics = sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)))
		stops = append(stops, metrics.Shutdown)
	}
	if c.logs.enabled {
		var exp sdklog.Exporter
		if c.logs.protocol == "grpc" {
			exp, err = otlploggrpc.New(ctx)
		} else {
			exp, err = otlploghttp.New(ctx)
		}
		if err != nil {
			return fail()
		}
		logs := sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)))
		stops = append(stops, logs.Shutdown)
		logger = slog.New(&safeHandler{local: local, remote: otelslog.NewHandler("github.com/api-mittens/looster", otelslog.WithLoggerProvider(logs))})
	}
	if traces != nil {
		otel.SetTracerProvider(traces)
	} else {
		otel.SetTracerProvider(tp)
	}
	if metrics != nil {
		otel.SetMeterProvider(metrics)
	} else {
		otel.SetMeterProvider(mp)
	}
	return logger, shutdown, nil
}
