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
The bounded reader also selects payload length. It does not return an oversized
BLOB chunk to the Go application. A task payload response has an 8 MiB text
budget, at most 200 sessions, and bounded run and log pages.
The checksum check applies before the API returns payload text.

`TestPayloadLayoutPlanProbe` uses the pinned engine and extension in a disposable
catalog. It inserted 8,192 rows with 8 MiB of repeated payload text for each
layout. It requested three SHA-256 keys with the production `IN` query shape.
`CREATE INDEX` returned `DuckLake does not support indexes`.
The primary-key probe returned that PRIMARY KEY/UNIQUE constraints are not
supported. `SET SORTED BY (payload_key)` succeeded in this version.

| Layout | Stored files | Stored file bytes | Files read in plan | Table scan rows | First native bytes read | Second native bytes read |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| No partition | 1 | 550,565 | 1 | 8,192 | 1,951 | 0 |
| 256 key buckets | 256 | 994,012 | 256 | 8,192 | 494,080 | 0 |
| Sorted by key | 1 | 507,243 | 1 | 8,192 | 1,951 | 0 |

The bucket files were 3,000 to 4,768 bytes each.
The plans did not prune the bucket files for this `IN` predicate.
The cumulative native scan counter was 81,920 for each profiled query.
That aggregate counter is not the table scan operator's 8,192-row count.
The first and second profiled reads took 2.549/2.009 ms for the plain layout,
8.036/5.793 ms for the bucket layout, and 2.313/1.883 ms for the sorted layout.
These are local cached-process readings. They are not cold S3 IO measurements.
The native byte counters are not total OS or network byte counters.
The repeated payload text compresses well and does not represent all task logs.

The probe shows a small-file cost in the existing 256-bucket layout.
It does not justify an automatic production layout change.
A safe follow-up must measure the production file distribution first.
Use a disposable catalog copy with representative payload sizes and key counts.
Compare equality, `IN`, sorting, and supported compaction on that copy.
Check file pruning and byte counters for both new and existing files.
Before any production rewrite, record the catalog snapshot and all key/checksum
pairs. Keep payload keys and PostgreSQL references unchanged. Test old and new
readers, concurrent writes, restart, and rollback on the copy. Obtain approval
for a separate migration. This PR performs none of those production changes.

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

The full project suite passed with a disposable PostgreSQL URL and `-count=1`.
The Node suite passed 43 tests. The lazy web storage race tests passed.
The default full project suite also passed without the database test variable.
Tests with other opt-in settings can still be skipped by that default command.

`TestTaskPayloadHTTPIntegration` passed with real PostgreSQL, the native
DuckLake engine, and the loopback S3 fixture. It created its own database and
removed that database after the test. It used 205 runs and 205 logs for one
task, plus a different task with a different payload key.

- Initial metadata reads used zero payload reader calls.
- Metadata still returned while the payload reader failed or was held open.
- The first task detail metadata response took 0.420 ms in the local fixture.
- Full payload responses took 6.416 ms and 5.699 ms. Each returned 50 runs,
  200 logs, and one session. Each response was 60,755 bytes.
- Each full payload response used one bounded batch call with one deduplicated
  key and zero single-key reads. The 302 reference fields shared that key.
- The one-key native read fits in one SQL query chunk. No unrelated task key
  reached the payload reader. The 600-key unit test crosses the 512-key chunk
  boundary and checks the byte budget across chunks.
- Independent run and log cursors excluded the prior records.
- A native payload write and an agent session-log append completed while HTTP
  reads continued. A separate process read both the original committed payload
  and the new committed payload from the shared catalog and S3 fixture.
- The S3 fixture observed GET: 8, PUT: 3, and ranged GET: 8.

These timings separate HTTP metadata completion from full payload completion.
They do not measure browser paint. The fixture has repeated short payload text.
It is not a production latency or large-payload benchmark.

## Requirement map

The final route audit also changed attachment lookup, session-log task lookup,
and comment creation checks to metadata reads. An HTTP test uses a repository
that rejects full task reads. Attachment lookup reports the missing object store,
and comment creation succeeds with zero payload calls.

| Requirement | Observed result | Remaining limit |
| --- | --- | --- |
| 1. PostgreSQL-only initial metadata | Public HTTP checks pass with no payload reader, with a failed reader, and with a delayed reader. List and graph retain PostgreSQL signal identities. Namespace startup is lazy. | Browser first paint is not measured. |
| 2. Separate section loading | The Node suite checks pending states, empty states, error/retry, retained content, task switching, cancellation, and reduced-motion CSS. | Actual animation, focus, and layout need the built-in browser. |
| 3. Selective bounded reads | The real HTTP fixture returns bounded pages with one deduplicated batch and no unrelated key. Byte-limit, checksum, 600-key chunk, and history cursor tests pass. | Production payload completion time is not measured. |
| 4. Supported layout and lookup options | Pinned runtime probes reject indexes and primary keys, accept sorting, and record file counts, sizes, plans, and native byte counters. PostgreSQL lookup plans are recorded above. | Production file distribution and cold S3 IO remain open. No production layout change was made. |
| 5. Write and visibility safety | Immutable payload tests, committed writes, concurrent agent log writes and HTTP reads, a separate reader process, and close/cancellation race checks pass. | The S3 fixture does not establish real-cloud IAM/TLS behavior. |

## External validation limits

Built-in browser automation could not start Brave.
The browser tool reported that Brave was not running.
The normal open tool then reported that browser launch was suppressed by the harness.
No browser bridge was installed, and no other browser driver was used.
Browser paint time, actual animation, focus retention, and layout remain unverified.
Production full payload completion time and controlled cold-cache timings remain unverified.
Production S3 byte counts, production small-file distribution, and production
query plans are not established by the local fixture.
Do not treat the local tests as full acceptance of these checks.
