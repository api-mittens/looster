# Process telemetry setup

Call `Setup` once, before worker or HTTP work starts. Use the returned logger.
Stop work before you call shutdown. Shutdown uses a fresh context with a shared
five-second limit. It is safe to call shutdown more than once.

No exporter starts when export settings are absent. Resource settings alone do
not start an exporter. A shared endpoint starts all three signals. A signal
endpoint starts that signal. An explicit `none` stops that signal, even with an
endpoint. `OTEL_SDK_DISABLED=true` stops all signals. Empty values use defaults.

Example:

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 \
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf looster serve
```

Set `OTEL_LOGS_EXPORTER=none` to stop log export. Set each signal exporter to
`none` to keep all telemetry local. Exporter values are `otlp` and `none` only.
Lists, `console`, `prometheus`, and other exporter values are not supported.
Protocols are `grpc` (default) and `http/protobuf`. HTTP JSON is not supported.

## Supported settings

The package reads these settings:

- `OTEL_SDK_DISABLED`
- `OTEL_TRACES_EXPORTER`, `OTEL_METRICS_EXPORTER`, `OTEL_LOGS_EXPORTER`
- `OTEL_EXPORTER_OTLP_PROTOCOL` and
  `OTEL_EXPORTER_OTLP_{TRACES,METRICS,LOGS}_PROTOCOL`
- `OTEL_PROPAGATORS`: `tracecontext`, `baggage`, `none`, or a comma-separated list

The official exporters read these settings. Here, `{SIGNAL}` means each of
`TRACES`, `METRICS`, and `LOGS`. Each signal setting takes priority over its shared
setting. HTTP shared endpoints get a signal path. HTTP signal endpoints must
include their full path.

- `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_{SIGNAL}_ENDPOINT`
- `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_EXPORTER_OTLP_{SIGNAL}_HEADERS`
- `OTEL_EXPORTER_OTLP_TIMEOUT`, `OTEL_EXPORTER_OTLP_{SIGNAL}_TIMEOUT`
- `OTEL_EXPORTER_OTLP_CERTIFICATE`, `OTEL_EXPORTER_OTLP_{SIGNAL}_CERTIFICATE`
- `OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE`,
  `OTEL_EXPORTER_OTLP_{SIGNAL}_CLIENT_CERTIFICATE`
- `OTEL_EXPORTER_OTLP_CLIENT_KEY`, `OTEL_EXPORTER_OTLP_{SIGNAL}_CLIENT_KEY`
- `OTEL_EXPORTER_OTLP_INSECURE`, `OTEL_EXPORTER_OTLP_{SIGNAL}_INSECURE`
- `OTEL_EXPORTER_OTLP_COMPRESSION`, `OTEL_EXPORTER_OTLP_{SIGNAL}_COMPRESSION`
- `OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE`
- `OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION`

The official SDK reads these settings:

- `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`
- `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG`
- `OTEL_BSP_MAX_QUEUE_SIZE`, `OTEL_BSP_MAX_EXPORT_BATCH_SIZE`,
  `OTEL_BSP_SCHEDULE_DELAY`, `OTEL_BSP_EXPORT_TIMEOUT`
- `OTEL_BLRP_MAX_QUEUE_SIZE`, `OTEL_BLRP_MAX_EXPORT_BATCH_SIZE`,
  `OTEL_BLRP_SCHEDULE_DELAY`, `OTEL_BLRP_EXPORT_TIMEOUT`
- `OTEL_METRIC_EXPORT_INTERVAL`, `OTEL_METRIC_EXPORT_TIMEOUT`
- `OTEL_METRICS_EXEMPLAR_FILTER`
- `OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT`, `OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT`,
  `OTEL_SPAN_ATTRIBUTE_VALUE_DEPTH_LIMIT`
- `OTEL_SPAN_EVENT_COUNT_LIMIT`, `OTEL_EVENT_ATTRIBUTE_COUNT_LIMIT`
- `OTEL_SPAN_LINK_COUNT_LIMIT`, `OTEL_LINK_ATTRIBUTE_COUNT_LIMIT`
- `OTEL_LOGRECORD_ATTRIBUTE_COUNT_LIMIT`,
  `OTEL_LOGRECORD_ATTRIBUTE_VALUE_LENGTH_LIMIT`,
  `OTEL_LOGRECORD_ATTRIBUTE_VALUE_DEPTH_LIMIT`

Timeout and interval settings use milliseconds. The SDK uses bounded batch
queues. The package does not set a blocking queue option. The SDK can use its
default when a numeric setting is invalid. This package does not claim support
for every OTel environment variable. Experimental Go-specific settings are not
part of this package contract.

The supplied service name is the default. `OTEL_SERVICE_NAME` takes priority.
The resource has `service.version` from the supplied version. Callers must use
`looster-serve` and `looster-web` as their defaults.

## Log data rules

Local stderr records keep their message and attributes. Export records use the
fixed body `Application log`. This prevents private text in a dynamic message
from leaving the process. Export includes only string `job_id`, `task_id`,
`agent`, `session_id`, and `namespace` values of at most 256 bytes. Callers must
put only identifiers in these fields. Export also includes bounded `operation`
and `outcome` values. Grouped fields, all other fields, error objects, and custom
log values do not enter the exporter. The bridge supplies trace and span IDs
from the call context. The local bridge record also has these IDs.

SDK errors use a fixed warning on stderr only. They never use the bridge. Export
connection failures do not change application results. Setup errors return a
local logger and a shutdown function. Callers can continue with that logger.
Resources are operator input. Do not put private data in resource attributes.

This package does not filter spans or metrics from other instrumentation.
Callers must not add private data to those signals. It does not add worker or
HTTP instruments. Those belong to their calling packages.

## Focused checks

Run `go test -race ./internal/telemetry`. The tests start local OTLP receivers
for both protocols. They check actual trace, metric, and log records, resources,
log correlation, private value filters, failed export, and repeat shutdown.
No external collector or database is required.
