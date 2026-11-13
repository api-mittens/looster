# PR #36 review: storage latency and missing AFTER handoffs

Reviewed November 13, 2026. PR head: `7cb7c0c`. Current remote default: `master` at `ac61cff`, not `main`. PR merge base: `ec1fb19`.

## Verdict

**Do not merge as-is.** The PR improves the web metadata boundary, but is incompatible with the current default branch and has two reproduced UI correctness defects. It does not remove the service's operational dependency on DuckLake. Overall rollout risk: **high** until integration and service-path checks pass.

This is a review, not a production fix. No database records, active behaviours, worker settings, tasks, or remote branches were changed. The original checkout and its unresolved merge conflicts were preserved. Review artifacts are on local branch `review/pr-36-storage-behaviour` in `/home/domdom/Code/looster-review-36`. A separate clean default-branch checkout is `/home/domdom/Code/looster-review-main`.

## PR findings, ordered by priority

### 1. [P1] Integrate the new payload route with master's request-aware response writer

Locations: `internal/web/web.go:152`, `internal/web/task_metadata.go:33`.

Master's telemetry change (#35) changed the response writer to `func(http.ResponseWriter, *http.Request, any, error)`. The newly introduced `registerTaskPayloads` still requires `func(http.ResponseWriter, any, error)` and uses the old calling convention.

I attempted a local merge into current master. Git reports conflicts in `internal/web/web.go` and `internal/web/session_logs.go`. After resolving only those textual conflicts by preserving both metadata reads and the request-aware writer, `go test -run '^$' ./internal/web ./internal/cli` fails:

```text
internal/web/web.go:152:34: cannot use write
(variable of type func(w http.ResponseWriter, r *http.Request, data any, err error))
as func(http.ResponseWriter, any, error) value in argument to registerTaskPayloads
```

Update the new helper and its calls to accept/pass the request, resolve conflicts without discarding #35, and test the integrated tree. Passing tests on the PR head alone do not validate the merge result. The test merge was aborted afterward, leaving the separate master checkout clean.

### 2. [P2] Update older-log pagination when a refreshed first page gains history

Location: `internal/web/static/app.js:376-379`.

`loadPayloads` records the server's older-log cursor only when `logsLoaded` is false. Open a task with no logs, then let more than 200 logs arrive before its next successful refresh. The response contains `has_more_logs: true` and a nonzero cursor, but the UI keeps `logsHasMore: false` and cursor zero. Older logs become unreachable until the dialog is reopened.

The same loss can occur if a previously complete short history grows by more than one page between reads. Fix cursor reconciliation to preserve explicitly loaded history while also exposing newly detected gaps, rather than unconditionally ignoring refreshed pagination metadata.

Offline characterization: `node docs/pr-36-review-repro.mjs`, first case. This executes the actual frontend methods in a minimal VM harness, not a real browser.

### 3. [P2] Do not let a slow payload response overwrite newer task metadata

Location: `internal/web/static/app.js:382`.

Task detail metadata and lake payload requests now complete independently. A payload request snapshots an old description, waits on lake reads, and can finish after a subsequent metadata refresh has rendered a newer description. Its task-ID/dialog guard still passes, so it writes the old description back into the panel. The metadata/payload split should keep description ownership with the metadata path or check a metadata revision before accepting overlapping fields.

Offline characterization: `node docs/pr-36-review-repro.mjs`, second case. A pending real `loadPayloads` call demonstrably overwrites a newer rendered description for the same task.

## Confirmed live explanation for the missing analyst/reviewer handoff

Read-only SQL inspection was performed against the configured database using explicit `BEGIN READ ONLY`, a five-second statement timeout, and `ROLLBACK`. No lake payload was needed for these checks.

The active rule rows include both unconditional rules:

```text
completed | coder | analyst
completed | coder | reviewer
```

The affected coder jobs did not complete successfully:

| Task | Job | Started, UTC on November 13 | Finished, UTC | Outcome | Duration |
|---|---:|---|---|---|---|
| TSK-79 | 1253 | 14:53:39 | 15:53:43 | failed | 1:00:04 |
| TSK-80 | 1256 | 15:28:30 | 16:28:35 | failed | 1:00:04.9 |
| TSK-82 | 1255 | 15:11:10 | 16:11:14 | failed | 1:00:04 |

All three errors reference the same SHA-256 key:

```text
05e510230f2518b842597cecaaf0106c48892735f561e532b9c3ab4ffa46c72d
```

Locally hashing the exact string `context deadline exceeded` yields that key. This identifies the referenced error by the application's content-addressing convention without retrieving the blob. The live worker advertises `run_timeout: 1h0m0s`, concurrency 10, and a one-second poll interval. TSK-80 has no `agent_stopped` events in the inspected state, despite its published PR comment. Job 1256 persisted 538 `agent_output` events over its run.

The implementation explains the absent handoff:

1. `internal/app/worker.go` treats a run deadline as a failure.
2. `internal/postgres/queue.go:278-284` emits `agent_stopped` only from successful non-notification completion of an open task.
3. `internal/postgres/migrations/013_manifests.sql:87-93` dispatches `AFTER ... COMPLETED` from `agent_stopped`.
4. A failed run emits `agent_failed`, which correctly does not trigger successful-completion rules.

**Conclusion:** for these jobs, the rules exist but their triggering success event never occurs. This is not evidence that DuckLake corrupted the behaviour rules. The exact share of the one-hour runtime attributable to storage, model execution, tool work, or other delays is not established by this inspection. Increasing the timeout alone is not a demonstrated storage fix. Do not emit a success event for failed work just to make reviewer jobs appear.

There is also a separate policy distinction: successful notification jobs emit `notification_handled`, not `agent_stopped`. TSK-80 coder job 1245 was a notification and also timed out. If notification runs are expected to launch AFTER chains, that requires an explicit, deduplicated policy change with loop-prevention tests. It is not the explanation for failed non-notification job 1256.

## Storage/service findings outside this PR's changes

These are existing architectural dependencies, not newly introduced PR regressions, except for the explicitly identified new lazy-adapter issue below.

### [P1] Task-status polling hydrates every log body

`internal/app/worker.go:275` invokes `Repo.Task` up to once per second for each running task, solely to inspect status. `internal/postgres/store.go:97-104` implements `Task` by reading task metadata plus `TaskSessionLogs`. `internal/postgres/session_logs.go` loads and hydrates all logs for the task, without pagination.

Consequently a task with referenced logs turns a status check into repeated historical lake reads. If that read blocks, shutdown waits for the watcher to return. The post-run closed-task check also calls the full `Task` method. Use an explicit metadata/status-only interface for control decisions. The full context/payload reads needed to prepare an agent prompt should remain deliberate, not implicit in every status lookup.

### [P1] Streaming capture waits on remote lake commits

`internal/jcode/jcode.go` synchronously invokes the output callback while consuming NDJSON. `internal/postgres/queue.go:207-215` externalizes every output, and externalizes a structured final summary separately. `externalize` waits for `PayloadStore.Put`, whose acknowledgement requires a remote lake commit. A slow capture blocks further stdout consumption and can backpressure the child process. Lake failures can terminate a harness run. `Archive` and the object-store archive are also prerequisites to `Complete`.

The code **already uses the DuckDB Appender** in `internal/ducklake/payload_query.go:193-214`. The problem is not simply a missing Appender. A serial emitter waits for each commit, so its next output cannot join the current batch. The ten-millisecond grouping window can aggregate concurrent callers but cannot batch sequential outputs blocked behind their own acknowledgements.

Recommended architecture: persist operational events and an outbox atomically to PostgreSQL, acknowledge that durable transaction, and export payload batches independently. Preserve crash recovery, retries, checksums, and cross-host visibility. Never switch to acknowledging a host-local spool while immediately publishing a remote-only reference. Keep an authoritative PostgreSQL body or remotely accessible fallback until export is durably complete.

### [P1] Failure recording itself depends on lake availability

`internal/postgres/queue.go:288-292` externalizes the error before updating the job/session or inserting the failure event. Worker cleanup gives this path five seconds. A lake outage can therefore prevent the service from recording its own operational failure, leaving misleading state. Store operational error/status data transactionally in PostgreSQL and archive it asynchronously if required.

### [P2] CLI metadata operations still eagerly start DuckLake

`internal/cli/cli.go:115-121` makes startup lazy only for the `web` command. Normal task listing/checking/commenting/status commands and behaviour inspection still require `openPayloadStore` before their actual operation. Some task listing/show paths also retain full log hydration. The web improvements should not be presented as full CLI/service decoupling. Use command capabilities and explicit metadata reads, while keeping genuine payload commands payload-capable.

### [P2] The new lazy adapter serializes entire payload operations

`internal/cli/web_payloads.go:36-75` holds its gate not just for initialization but through the entire `Put`, including the remote commit wait. Concurrent web writes cannot enter the underlying payload queue together, reducing its existing group-commit opportunity. A slow read also holds up subsequent payload writes through this adapter. The native engine already has its own session serialization, but the extra outer gate prevents even queue admission and pending-overlay reads from progressing. Separate initialization/lifetime safety from operation admission, with explicit close/active-operation tracking.

### Layout concerns require measurement, not blind indexing

`internal/ducklake/payload_init.go` creates 256 hash buckets and explicitly sets `data_inlining_row_limit` to zero. A small batch spread across buckets can create many small files. `putBatch` additionally queries existing keys before appending, so write latency also includes key lookup work. The PR's own probe reports that the pinned bucketed fixture did not prune files for its tested key lookup. That is fixture evidence, not a measured production query plan.

Official DuckLake documentation states that DuckLake tables do not support ordinary indexes or primary keys. DuckDB's native-table index support must not be assumed to apply to the attached lake. Official documentation also describes data inlining for small changes, so frequent small changes are not inherently unsupported, but this application deliberately disables that mechanism. Do not enable it or repartition production without testing the pinned versions, catalog load, rollback, and existing data.

Relevant official documentation consulted:

- DuckLake Introduction: `https://ducklake.select/docs/stable/duckdb/introduction`
- DuckLake Data Inlining: `https://ducklake.select/docs/stable/duckdb/advanced_features/data_inlining`
- DuckDB Appender: `https://duckdb.org/docs/current/data/appender`

Use PostgreSQL indexes for operational queries. For the lake, measure file counts/sizes, pruning, compaction, and batch sizes before selecting a migration.

## Recommended order of work

1. Fix the PR integration/build blocker and its two reproduced UI defects. Retain the useful metadata-first web boundary.
2. Replace watcher/control-path full task reads with metadata/status-only reads. Prove zero payload access under unavailable/delayed lake conditions.
3. Remove eager lake startup from metadata-only CLI operations. Exercise real command entrypoints, not only repository methods.
4. Decouple capture, operational errors, and completion/handoffs from archival transport through a durable PostgreSQL outbox. Keep successful-completion and archive status separately observable.
5. Add an end-to-end worker test with real rules: coder success creates analyst/reviewer jobs exactly once, coder timeout creates no success handoff, notification policy is explicit, and archive unavailability does not erase operational outcome.
6. Measure lake query and write latency, commit batch distributions, file sizes/counts, queue age, PostgreSQL pool waits, and browser first paint with realistic concurrency. Then consider layout/inlining/compaction changes.

## Follow-up: real HTTP acceptance against the configured database

The opt-in project test `TestLiveMetadataHTTP` was run twice, then strengthened to pass the actual repository through `app.WithPermissions`, read complete bounded response bodies, validate JSON, and include comment reads. It starts the real project HTTP handler on a loopback TCP listener against the configured PostgreSQL database, without attaching any payload store and without invoking migrations or mutations. No repository stub, mock database, or copied endpoint implementation is used.

Command:

```sh
LOOSTER_METADATA_HTTP_CHECK=1 LOOSTER_NAMESPACE=default go test -v -count=1 -timeout=90s ./internal/web -run '^TestLiveMetadataHTTP$'
```

Observed strengthened run, November 13, 2026:

| Public HTTP route | Status on both requests | Complete JSON bytes | Request 1 | Request 2 |
|---|---:|---:|---:|---:|
| `/api/board/list` | 200 | 97,651 | 3.627 s | 2.666 s |
| `/api/board` | 200 | 146,206 | 514 ms | 353 ms |
| `/api/tasks/TSK-80` | 200 | 5,137 | 459 ms | 320 ms |
| `/api/tasks/TSK-80/events?kind=comment` | 200 | 4,873 | 372 ms | 113 ms |
| `/api/status` | 200 | 40,341 | 164 ms | 126 ms |

All ten responses passed complete-body JSON checks. This directly verifies that the PR's real metadata/list/comment-read/status HTTP paths work against existing data with no lake reader, through the application's permission boundary. It does not validate the CLI startup path, comment writes, payload completion, or an end-to-end worker execution.

**Additional observed issue:** list requests remain multi-second even with DuckLake entirely absent. Therefore not all current list latency can be assigned to lake IO. `internal/web/column_pages.go` awaits each status/agent column sequentially; each `ColumnTasks` call opens a transaction and issues count/page/signal queries before committing. Multiple sequential PostgreSQL round trips are a concrete candidate for investigation. This run did not isolate their individual cost, so it is not proof of exact latency attribution. Batch the per-column metadata fetch or measure its query/round-trip timings before choosing the optimization.

This is stronger acceptance evidence for the review, not evidence of a product speedup: no runtime product fix was made. No controlled before/after comparison is claimed. TSK-80's current log query returned zero session-log rows, so the watcher hydration problem is conditional on tasks that have logs and is not established as this particular task's timeout cause.

A real browser acceptance attempt was also made: the browser tool reports Firefox running but its live bridge unresponsive. Actual painting, focus, and visual pagination acceptance remain blocked by that bridge. The two frontend characterizations remain synthetic evidence and are not relabeled browser acceptance. Mutating production to test comments or dispatch was deliberately avoided, and the isolated native PostgreSQL/S3 fixture remains blocked by missing PostgreSQL server tools.

## Discoverer review evaluation

User-requested check completed against committed review head `1ec141f` and base `ac61cff`. Successful command, run from `/home/domdom/Code/discoverer`:

```sh
go run ./cmd/discoverer review /home/domdom/Code/looster --head review/pr-36-storage-behaviour --base origin/master --format json
```

JSON output is saved at `/home/domdom/Code/looster-review-36-discoverer.json` (about 1.2 MiB). It parses successfully and identifies the expected head/base/merge-base hashes. It covers 39 changed files, of which 35 are mapped, one is outside declarations, and three are unsupported-language. Both history and static analyses report 172 touched declarations. History has 10 advisory boundary conflicts and 231 total neighbor relationships; static has 19 advisory boundary conflicts and 126 total neighbors. Only the top 20 neighbor relationships per source are emitted with the default setting.

**Usefulness: useful as an architectural review map, not a correctness verifier.** It directs attention to `store.go`, CLI startup, and the web handler, matching the boundaries that need care in this change. Historical neighbors include `PayloadStore.Put/loop/Close` and session-history recovery tests, useful prompts to retain cross-process durability and retry coverage. Static neighbors include `WithPermissions`, `OpenNamespace`, and `EnsureNamespace`, supporting the need to exercise the permission and startup boundaries rather than only repository fakes.

It did not identify the response-writer compiler failure, the two reproduced UI bugs, the live one-hour timeout explanation, or the measured lake-independent list latency. The report explicitly describes its conflicts as advisory architectural feedback, not merge conflicts or required edits. Do not interpret the 29 boundary conflicts as 29 bugs. Some default top-neighbor slots are consumed by repeated links to the same declaration, and history promotes `TestProgressiveMetadataBrowser` from a file no longer present at head. That historical evidence is labeled, but it adds noise to an actionable review checklist.

Invocation friction observed:

1. The requested `go run ~/Code/discoverer ...` fails from the Looster module because the directory is outside that Go module. The Discoverer module root is also a library, not a main package. Its CLI entrypoint is `./cmd/discoverer`.
2. Looster's actual default branch is `origin/master`, not `origin/main`.
3. Running against the linked review worktree reports `origin/master` as missing even though `git rev-parse origin/master` succeeds there. Using the primary repository with an explicit review branch works without changing the primary checkout.
4. The successful command exits zero but prints connection-refused OpenTelemetry exporter warnings for localhost port 4317. `--otel-endpoint ''` is available to disable that optional exporter when no collector is running.

No Discoverer source was changed. No new correctness claim is based solely on its community/coupling graph.

## Validation and limits

- PR head: `go test -count=1 -timeout=5m ./internal/app ./internal/postgres ./internal/cli ./internal/ducklake ./internal/web` passed.
- PR head: `node --test internal/web/static/*.test.mjs` passed all 43 tests.
- Review reproducer: both characterization assertions passed and confirmed the two UI defects.
- Integration with current master: textual conflicts, followed by the concrete compiler failure above after minimal semantic conflict resolution.
- Current master: the same five targeted Go package suites passed on clean `ac61cff`.
- Database-backed tests require `LOOSTER_TEST_DATABASE_URL`; the targeted manifest and task-detail integration cases explicitly skipped because it was unset. Production was inspected read-only, never used as a test database.
- A native disposable integration run was attempted separately. Local PostgreSQL server tools are unavailable at the harness's expected path, so no full native PostgreSQL/S3 acceptance result is claimed.
- No controlled before/after production benchmark, actual browser first-paint test, trace-based latency attribution, production payload query plan, lake migration, service restart, or timeout change was performed. The live metadata HTTP measurements above are uncontrolled observational checks.
