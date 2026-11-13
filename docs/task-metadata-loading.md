# TSK-80 verification

## Scope

This change separates PostgreSQL task metadata from lake payload reads.
The web API keeps the task, session, and run field names.
The payload endpoint does not return storage keys.
The change does not migrate production data or change the lake layout.

## Read paths

- `/api/board/list` reads task metadata and log signal metadata from PostgreSQL.
- `/api/board` reads task and session metadata from PostgreSQL.
- `/api/status` reads worker, agent, and session metadata from PostgreSQL.
- `/api/tasks/{id}` reads task metadata, session metadata, and tag context from PostgreSQL.
- `/api/tasks/{id}/events?kind=comment` reads PostgreSQL comments.
- `/api/tasks/{id}/payloads` reads a bounded task detail page and batches its payload references.
- Task attachments load through a separate request.
- Library, manifests, and system data do not delay board rendering.

The permission wrapper forwards the new read ports.
The existing CLI full reads remain available.
The web storage adapter starts DuckLake only for an explicit payload read or write.
A failed payload request must not replace completed task content.

## Test methods

`TestMetadataHTTPDoesNotReadPayloads` rejects full task and session reads.
It checks board, list, status, task detail, and comment HTTP routes.
It also checks task metadata after a payload failure.
`TestMetadataHTTPDuringDelayedPayload` holds a payload HTTP request open.
Metadata HTTP requests must finish while that request remains pending.
`TestTaskPayloadBoundsHTTP` checks invalid page bounds before any payload call.
`TestPermissionWrapperPreservesMetadataPort` checks the authorization boundary.

The Node tests use the actual UI methods with controlled request completion.
They check metadata-first rendering, retry, content retention, task switching,
cancellation, empty states, and reduced-motion CSS.
These tests are not a substitute for a browser check.

## Measurements on 2026-11-13

Read-only requests to the existing server returned:

| Route | Elapsed time |
| --- | ---: |
| `/api/board/list` | 19.669 s |
| `/api/board` | 9.022 s |
| `/api/tasks/TSK-80` | 4.601 s |

These are HTTP endpoint times, not native query stage times.
The server binary revision was not established by these requests.
The system had concurrent agent activity.
The readings are not controlled cold-cache measurements.

The new read-only HTTP test opens the selected PostgreSQL namespace without a
payload store. This makes any required lake payload read fail.
The first pass found a remaining list hydration call. The list request failed,
while board, task detail, and status returned metadata. This failure caused a
further code change. Final measurements follow below.

The repeated check passed with no payload store installed:

| Route | First request | Second request |
| --- | ---: | ---: |
| `/api/board/list` | 2.393 s | 2.001 s |
| `/api/board` | 0.279 s | 0.188 s |
| `/api/tasks/TSK-80` | 0.375 s | 0.228 s |
| `/api/status` | 0.231 s | 0.146 s |

These results measure metadata response completion, not browser paint time.
The first request used a new HTTP test server and PostgreSQL pool.
The second request reused that pool. Neither request used a controlled cold
PostgreSQL or S3 cache. The old and new list requests used the same default page
parameters. List metadata still needs about two seconds on this connection.
The test does not claim that this remaining time comes from a specific query.

## Storage and write safety

The shipped engine is DuckDB `v1.5.2`.
The shipped DuckLake version is `1.0`, with signed extension ID `415a9ebd`.
The engine buffer limit is `256MiB`. This is not a total process memory limit.
The existing payload table creation uses `bucket(256, payload_key)`.
Existing catalogs keep their current partition layout.
The production SQL reader requests only `payload_key` and `payload`.
It uses parameterized key predicates and chunks of at most 512 keys.

Read-only `EXPLAIN (ANALYZE, BUFFERS)` probes for TSK-80 found these paths:

| Lookup | Observed plan | Rows | Shared buffer hits | Execution time |
| --- | --- | ---: | ---: | ---: |
| Task by ID | `tasks_pkey` index scan | 1 | 2 | 0.038 ms |
| Recent task log metadata | backward `session_logs_task_idx` scan | 0 | 1 | 0.055 ms |
| Task or context-task run page | sequential scan and sort | 3 | 126 | 0.303 ms |

The run probe removed 663 rows by its filter.
It did not select summary or error payload columns.
The current task index does not prove an indexed path for the context-task OR
predicate. No new production index was added from this small measurement.
The probes are narrower than the complete board query and do not explain its
full endpoint time. The task has no session logs in this measured namespace.

`./scripts/test-ducklake.sh` passed on 2026-11-13.
It used a disposable PostgreSQL cluster and a loopback S3 protocol fixture.
The fixture observed GET: 2, POST: 1, PUT: 1, and ranged GET: 2.
The compiled CLI checks retained the application database and namespace sentinels.
They checked isolated metadata schemas and paths after repeated initialization.
This check does not establish real-cloud IAM or TLS compatibility.

Race checks passed for blocked reads with concurrent commits and safe close,
group commit, delayed writes, cancellation, and close behavior.
The payload checksum and committed-write paths remain in use.

## External validation limits

Built-in browser automation could not start Brave.
The browser tool reported that Brave was not running.
The normal open tool then reported that browser launch was suppressed by the harness.
No browser bridge was installed, and no other browser driver was used.
Browser paint time, actual animation, focus retention, and layout remain unverified.
Full payload completion time and controlled cold-cache timings remain unverified.
Production S3 byte counts, production small-file distribution, and production
query plans are not established by the local fixture.
Do not treat the local tests as full acceptance of these checks.
