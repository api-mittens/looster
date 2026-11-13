# OpenTelemetry

`looster serve` and `looster web` can export traces, metrics, and logs through OTLP.
Other commands do not start exporters. No exporter starts by default.
The default service names are `looster-serve` and `looster-web`.
`OTEL_SERVICE_NAME` sets a different name. The release version sets `service.version`.

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 \
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf looster serve
```

Use the same environment settings for `looster web`.
Set `OTEL_LOGS_EXPORTER=none` to stop log export.
Set `OTEL_SDK_DISABLED=true` to stop all export.
A shared endpoint enables all signals unless an explicit exporter value is `none`.
A signal endpoint or explicit `otlp` exporter enables only that signal.
Resource settings alone do not enable export.
See [the configuration reference](../internal/telemetry/README.md) for supported settings and precedence.

## Metrics

| Instrument | Type | Unit | Dimensions |
| --- | --- | --- | --- |
| `looster.job.attempts` | Counter | Count | outcome |
| `looster.job.duration` | Histogram | s | outcome |
| `looster.job.active` | Up/down counter | Count | None |
| `looster.worker.capacity` | Gauge | Count | None |
| `looster.operation.duration` | Histogram | s | operation, outcome |
| `looster.operation.errors` | Counter | Count | operation, outcome |
| `looster.scheduler.dispatched` | Counter | Count | None |

Job outcomes are `completed`, `failed`, `timeout`, `interrupted`, and `task_closed`.
Operations are `schedule`, `claim`, `heartbeat`, `capture`, `archive`, and `complete`.
Each claimed attempt counts once, including archive recovery.
A failed completion does not count as success.
Active jobs decrease on every exit.
Empty claims produce metrics but no spans or logs.
The official HTTP instrumentation supplies request duration metrics.
A separate `http.server.active_requests` up/down counter uses unit `{request}`
and the bounded HTTP method only. The selected HTTP package does not supply it.
HTTP dimensions use bounded methods, route templates, and status codes.
Do not use these instruments to infer queue wait time or queue depth.
The job model has no reliable enqueue timestamp for these measurements.

## Traces and logs

Each claimed attempt starts an independent `looster.job.process` root span.
Child spans show preparation, execution, archive, capture failures, and completion.
Normal interruption and task closure are expected outcomes.
Timeout and failed operations set an error status without private error text.
Job, task, agent, namespace, and session identifiers belong in spans and logs only.
They do not belong in metric dimensions.
No stored carrier connects an HTTP request to a later job attempt.
Child agent commands do not inherit `TRACEPARENT`, `TRACESTATE`, or `BAGGAGE`.

HTTP spans use route templates and accept W3C trace context.
Unknown routes use `/unmatched`.
The instrumentation does not export raw paths, queries, headers, or client addresses.
This scope covers the Go server, not browser JavaScript.

Local stderr logs remain available. Exported logs use a fixed safe event message
or `Application log`. Only approved identifiers and bounded fields enter export.
Context-aware records carry trace and span IDs.
Prompts, task text, output, history, credentials, SQL, object keys, and full commands
must not enter telemetry. Operator resource attributes must also follow this rule.
Database audit records remain the source of persistent task and session events.

Workers stop before telemetry shutdown. HTTP shutdown waits for active requests.
Export shutdown has a fresh context with a five-second limit.
Exporter connection failures do not stop business operations.
SDK export errors go to local stderr only to prevent log export recursion.

## Verification

```sh
go test ./...
go test -race ./internal/telemetry ./internal/app ./internal/web ./internal/jcode
```

The telemetry tests start local OTLP receivers for gRPC and HTTP/protobuf.
They check received records for all three signals, not only exporter startup.
The tests also check disabled export, configuration, resources, private data filters,
log correlation, export failure, and bounded shutdown.
Worker tests check outcome counts, child spans, and active-job balance.
HTTP tests check route templates, namespace requests, propagation, private data,
and streaming response support.
Tests that need external storage use the project test-service configuration.
A skipped storage test does not prove a live storage integration result.
