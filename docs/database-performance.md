# Database performance: TSK-27 index evidence

## Scope and reproduction

Migration `016_query_indexes.sql` was selected from real PostgreSQL plans, not
from table-name heuristics. No production connection or data was used.

Recorded November 3, 2026, 17:55:42-17:56:09 UTC, PostgreSQL 18.6,
x86_64 Linux, GCC 16.2.1. Planner settings: `shared_buffers=128MB`,
`work_mem=4MB`, `effective_cache_size=4GB`, `random_page_cost=4`,
`max_parallel_workers_per_gather=2`, `jit=on`. These are local disposable-server
measurements, not production latency promises or statistically sampled medians.

```sh
LOOSTER_TEST_DATABASE_URL=postgresql://looster_test@127.0.0.1:55427/looster_disposable \
LOOSTER_QUERY_PLANS=1 \
go test ./postgres -run '^TestQueryIndexPlans$' -count=1 -v

LOOSTER_TEST_DATABASE_URL=postgresql://looster_test@127.0.0.1:55427/looster_disposable \
LOOSTER_QUERY_PLANS=1 \
go test ./postgres -run '^TestQueryPlansTaskListAndGenericLogs$' -count=1 -v

LOOSTER_TEST_DATABASE_URL=postgresql://looster_test@127.0.0.1:55427/looster_disposable \
go test ./postgres -run '^TestQueryIndexes' -count=1 -v
```

The supplied login needs `CREATEDB`. `queryPlanDatabase` uses
`legacyUpgradeDatabase` to create and clean up a separate randomly named database.
The supplied database is only the administrative connection, never a fixture target.
The namespace test uses the existing `manifestTestStore` disposable-schema helper.

The fixture in `postgres/query_plans_test.go` contains:

| Relation | Rows | Distribution |
| --- | ---: | --- |
| agents / tags / rules | 1,000 each | Distinct owners, one TODO rule per agent |
| tasks | 100,000 | 80-byte description |
| task_tags | 100,000 | 100 assignments per tag |
| memories | 200,000 | 100,000 tag-owned and 100,000 agent-owned, 120-byte bodies |
| sessions | 100,000 | 100 sessions per agent |
| events | 200,000 | Two per task, 90% output, 10% comment |
| attachments | 200,000 | Two per task, distinct event and object key |
| jobs | 200,000 | 80,000 active, 120,000 terminal, 1,000 agents |

Jobs alternate task-owned and context-task-owned notifications. Context IDs cycle
through 40,000 tasks, so every active session has two queued candidates and
the full claim query actually rejects later jobs. Terminal jobs reference 1,000
rules, exercising the part of FK maintenance that an active-only index cannot
cover. This synthetic distribution models a large multi-owner installation, not
a claim about a particular customer's distribution. It does not model severe
owner skew, every active state, standalone NULL contexts, concurrent production
load, or large history blobs. Queue behavior tests separately cover state,
NULL-context and locking correctness, not production-scale distributions.

Only local user audit/dispatch triggers are disabled during fixture bulk loading,
then re-enabled. FK/check constraints remain enabled. The test runs `ANALYZE`,
executes each `EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)` once to warm up and then
records the second execution. It measures version 015 first, applies the actual
embedded 016 migration through `Migrate`, then warms and measures again. No
`enable_seqscan=off`, planner hints, fabricated costs, or forced index use.
A warm-up is not a promise that the entire database fits shared buffers.

## Concrete before/after results

Times below are PostgreSQL **Execution Time**, in milliseconds, excluding planning.
Buffers are execution-root `shared hit` counts from the final recorded run, not
sums of parent and child nodes. All displayed probes in this run were cache hits.
`Bitmap` means bitmap index scan plus heap scan. Rows are actual returned rows
and match before and after.

| Probe / actual predicate | Rows | Before plan | Before ms / buffers | After plan / selected index | After ms / buffers |
| --- | ---: | --- | ---: | --- | ---: |
| Reverse task tag, `tag_id=777` | 100 | Seq Scan | 9.244 / 541 | Bitmap `task_tags_tag_idx` | 0.115 / 102 |
| Tag memories, tag name `tag/ahhh`, `ORDER BY id` | 100 | Parallel Seq Scan + Gather Merge | 17.984 / 4,464 | Bitmap `memories_tag_id_idx` + Sort | 0.179 / 106 |
| Agent memories, `agent777`, `ORDER BY id` | 100 | Parallel Seq Scan + Gather Merge | 18.465 / 4,521 | Bitmap `memories_agent_id_idx` + Sort | 0.309 / 104 |
| Agent sessions, `agent777`, ordered by task | 100 | Seq Scan + Sort | 11.500 / 736 | Bitmap `sessions_agent_idx` + Sort | 0.153 / 102 |
| Task attachments, `task_id=777`, `ORDER BY id` | 2 | Parallel Seq Scan + Gather Merge | 19.367 / 1,908 | Index Scan `attachments_task_id_idx` | 0.013 / 5 |
| Attachment event FK probe, `event_id=777` | 1 | Seq Scan + LockRows | 17.437 / 1,871 | Index Scan `attachments_event_idx` + LockRows | 0.055 / 5 |
| Active predecessor, agent777/context777/id<79000 | 2 | `jobs_pending_idx`, filter 78,997 rows | 19.808 / 1,347 | Index Scan `jobs_active_session_idx` | 0.020 / 5 |
| Jobs task FK probe, `task_id=778` | 5 | Seq Scan + LockRows | 18.479 / 2,811 | Index Scan `jobs_task_idx` + LockRows | 0.065 / 12 |
| Jobs agent FK probe, `agent777` | 200 | Seq Scan + LockRows | 22.983 / 3,006 | Bitmap `jobs_agent_idx` + LockRows | 0.530 / 403 |
| Jobs rule FK probe, `schedule_rule_id=777` | 120 | Seq Scan + LockRows | 30.315 / 2,926 | Bitmap `jobs_schedule_rule_idx` + LockRows | 0.376 / 242 |
| Full queue candidate anti-join, agent777 | 40 | Parallel Hash Anti Join | 49.167 / 3,135 | Nested Loop Anti Join, active index on both sides | 0.494 / 363 |
| Task activity count, task780 | 1 aggregate, 2 input | Index Only Scan, existing 011 | 0.033 / 4 | Same existing index | 0.023 / 4 |
| Task latest activity, task777 | 1 | Index Only Scan + Limit, existing 015 | 0.013 / 4 | Same existing index | 0.014 / 4 |
| Rules target FK probe, agent777 | 1 | Seq Scan + LockRows | 0.185 / 11 | Unchanged | 0.254 / 11 |
| Rules source FK probe, agent777 | 0 | Seq Scan + LockRows | 0.125 / 10 | Unchanged | 0.095 / 10 |

The exact SQL for every row is in `measuredQueries`. FK probes use
`SELECT 1 FROM child WHERE fk=value FOR KEY SHARE`: representative leading-key
lookup/row-lock work, **not** a measured end-to-end DELETE or cascade benchmark.
This is conservative about attribution: it demonstrates the expensive lookup,
not all application/trigger work a delete might perform. In particular events
are append-only, so the attachment event probe is FK support evidence rather
than a suggestion to delete event history.

### Saved EXPLAIN ANALYZE BUFFERS excerpts

These are actual selected plan lines, with omitted nodes explicitly indicated.
The test's `-v` output prints the complete plans for every probe on each run.

Reverse tag lookup:

```text
BEFORE
Seq Scan on task_tags (actual time=0.072..9.212 rows=100.00 loops=1)
  Filter: (tag_id = 777)
  Rows Removed by Filter: 99900
  Buffers: shared hit=541
Execution Time: 9.244 ms

AFTER
Bitmap Heap Scan on task_tags (actual time=0.031..0.102 rows=100.00 loops=1)
  Recheck Cond: (tag_id = 777)
  Heap Blocks: exact=100
  Buffers: shared hit=102
  -> Bitmap Index Scan on task_tags_tag_idx (actual time=0.010..0.010 rows=100.00 loops=1)
       Index Cond: (tag_id = 777)
       Buffers: shared hit=2
Execution Time: 0.115 ms
```

Tag memories, with the owner-name InitPlan and sort details omitted:

```text
BEFORE
Gather Merge (actual time=14.199..17.929 rows=100.00 loops=1)
  Buffers: shared hit=4464
  ...
  -> Parallel Seq Scan on memories (actual time=0.118..9.334 rows=33.33 loops=3)
       Filter: (tag_id = (InitPlan 1).col1)
       Rows Removed by Filter: 66633
       Buffers: shared hit=4445
Execution Time: 17.984 ms

AFTER
Sort (actual time=0.150..0.157 rows=100.00 loops=1)
  Sort Key: memories.id
  Sort Method: quicksort Memory: 40kB
  Buffers: shared hit=106
  ...
  -> Bitmap Heap Scan on memories (actual time=0.042..0.128 rows=100.00 loops=1)
       Recheck Cond: (tag_id = (InitPlan 1).col1)
       Heap Blocks: exact=100
       Buffers: shared hit=106
       -> Bitmap Index Scan on memories_tag_id_idx (actual time=0.023..0.023 rows=100.00 loops=1)
            Index Cond: (tag_id = (InitPlan 1).col1)
            Buffers: shared hit=6
Execution Time: 0.179 ms
```

The agent-memory and session plans likewise use bitmap scans and a small sort.
Do not claim that the composite memory indexes eliminate sorting for this
fixture. The measured gain is owner selectivity, not removal of the sort.

Task attachments:

```text
BEFORE
Gather Merge (actual time=15.655..19.329 rows=2.00 loops=1)
  Buffers: shared hit=1908
  ...
  -> Parallel Seq Scan on attachments (actual time=4.369..11.946 rows=1.00 loops=2)
       Filter: (task_id = 777)
       Rows Removed by Filter: 99999
       Buffers: shared hit=1870
Execution Time: 19.367 ms

AFTER
Index Scan using attachments_task_id_idx on attachments (actual time=0.005..0.007 rows=2.00 loops=1)
  Index Cond: (task_id = 777)
  Buffers: shared hit=5
Execution Time: 0.013 ms
```

Predecessor lookup, the inner predicate of the anti-join:

```text
BEFORE
Index Scan using jobs_pending_idx on jobs earlier (actual time=0.211..19.783 rows=2.00 loops=1)
  Index Cond: (id < 79000)
  Filter: ((COALESCE(task_id, context_task_id) = 777) AND (agent_name = 'agent777'::text))
  Rows Removed by Filter: 78997
  Buffers: shared hit=1347
Execution Time: 19.808 ms

AFTER
Index Scan using jobs_active_session_idx on jobs earlier (actual time=0.009..0.011 rows=2.00 loops=1)
  Index Cond: ((agent_name = 'agent777'::text) AND (COALESCE(task_id, context_task_id) = 777) AND (id < 79000))
  Buffers: shared hit=5
Execution Time: 0.020 ms
```

Full candidate query uses the actual metadata-only projection from `Claim`, not
`history`. No FIFO, availability, NULL-context, or advisory-lock semantics change.
Before, the parallel hash build scans all 80,000 active rows:

```text
Limit (actual time=45.646..49.110 rows=40.00 loops=1)
  Buffers: shared hit=3135
  -> Gather Merge (actual time=45.644..49.102 rows=40.00 loops=1)
       Workers Planned: 1
       Workers Launched: 1
       Buffers: shared hit=3135
       -> Sort (actual time=42.460..42.466 rows=20.00 loops=2)
            Sort Key: j.id
            Buffers: shared hit=3135
            -> Parallel Hash Anti Join (actual time=31.713..42.431 rows=20.00 loops=2)
                 Hash Cond: ((COALESCE(j.task_id, j.context_task_id) = COALESCE(earlier.task_id, earlier.context_task_id)) AND (j.agent_name = earlier.agent_name))
                 Join Filter: (earlier.id < j.id)
                 Rows Removed by Join Filter: 60
                 Buffers: shared hit=3127
                 -> Parallel Index Scan using jobs_pending_idx on jobs j (actual time=0.281..10.825 rows=40.00 loops=2)
                      Filter: ((agent_name = ANY ('{agent777}'::text[])) AND (available_at <= now()))
                      Rows Removed by Filter: 39960
                      Buffers: shared hit=1572
                 -> Parallel Hash (actual time=30.523..30.524 rows=40000.00 loops=2)
                      Buckets: 131072 Batches: 1 Memory Usage: 5984kB
                      Buffers: shared hit=1555
                      -> Parallel Index Scan using jobs_pending_idx on jobs earlier (actual time=0.024..14.027 rows=40000.00 loops=2)
                           Buffers: shared hit=1555
Planning Time: 0.577 ms
Execution Time: 49.167 ms
```

After, both agent selection and each predecessor probe use the new index:

```text
Limit (actual time=0.439..0.449 rows=40.00 loops=1)
  Buffers: shared hit=363
  -> Sort (actual time=0.438..0.442 rows=40.00 loops=1)
       Sort Key: j.id
       Sort Method: quicksort Memory: 30kB
       Buffers: shared hit=363
       -> Nested Loop Anti Join (actual time=0.070..0.420 rows=40.00 loops=1)
            Buffers: shared hit=363
            -> Bitmap Heap Scan on jobs j (actual time=0.057..0.150 rows=80.00 loops=1)
                 Recheck Cond: ((agent_name = ANY ('{agent777}'::text[])) AND (state = ANY ('{pending,running,archiving}'::text[])))
                 Filter: (available_at <= now())
                 Heap Blocks: exact=80
                 Buffers: shared hit=83
                 -> Bitmap Index Scan on jobs_active_session_idx (actual time=0.026..0.026 rows=80.00 loops=1)
                      Index Cond: (agent_name = ANY ('{agent777}'::text[]))
                      Buffers: shared hit=3
            -> Index Scan using jobs_active_session_idx on jobs earlier (actual time=0.003..0.003 rows=0.50 loops=80)
                 Index Cond: ((agent_name = j.agent_name) AND (COALESCE(task_id, context_task_id) = COALESCE(j.task_id, j.context_task_id)) AND (id < j.id))
                 Buffers: shared hit=280
Planning Time: 0.678 ms
Execution Time: 0.494 ms
```

## Complete ListTasks and generic log-query evidence

A second run on November 3, 2026, 18:02:37-18:03:27 UTC uses the same fixture,
server settings and per-query warm-up. `TestQueryPlansTaskListAndGenericLogs`
reads the complete `ListTasks` statement directly from `queries/tasks.sql`,
including task columns, ordered tag arrays, lateral filtered counts, correlated
latest-activity lookup and final task ordering. It does not add a task filter or
LIMIT. Both phases return all **100,000 tasks**.

| Complete ListTasks | Execution ms | Root shared hits | JIT total ms | Plan |
| --- | ---: | ---: | ---: | --- |
| Version 015 | 1,800.636 | 1,222,094 | 190.032 | Nested Loop Left Join |
| Version 016 | 1,286.180 | 1,222,094 | 130.942 | Same Nested Loop Left Join |

**No board speedup is attributed to migration 016.** The plan, logical buffer
work, existing index choices and row counts are identical. Wall time and JIT
vary substantially on this shared machine. The earlier isolated count/maximum
probes establish efficient per-task access, not constant-time all-task listing.
The full query performs 100,000 count probes, 100,000 tag lookups/sorts, and
100,000 maximum-activity probes. It still has linear work in task count and
substantial repeated buffer accesses. Pagination or board-query restructuring
would be a separate, workload-driven change, not justification for another
near-duplicate events index here.

Selected actual plan lines (tag subtree omitted, exact buffer breakdown below):

```text
BEFORE full ListTasks
Nested Loop Left Join (actual time=189.034..1785.459 rows=100000.00 loops=1)
  Buffers: shared hit=1222094
  -> Index Scan using tasks_pkey on tasks (actual time=0.020..32.802 rows=100000.00 loops=1)
       Buffers: shared hit=2094
  -> Aggregate (actual time=0.004..0.004 rows=1.00 loops=100000)
       Buffers: shared hit=220000
       -> Index Only Scan using events_task_activity_idx on events (actual time=0.002..0.002 rows=0.20 loops=100000)
            Index Cond: (task_id = tasks.id)
            Heap Fetches: 20000
            Buffers: shared hit=220000
  ...
  SubPlan 3
    -> Result (actual time=0.004..0.004 rows=1.00 loops=100000)
         Buffers: shared hit=400000
         InitPlan 2
           -> Limit (actual time=0.003..0.003 rows=1.00 loops=100000)
                Buffers: shared hit=400000
                -> Index Only Scan using events_task_last_activity on events e (actual time=0.003..0.003 rows=1.00 loops=100000)
                     Index Cond: (task_id = tasks.id)
                     Heap Fetches: 100000
                     Buffers: shared hit=400000
Execution Time: 1800.636 ms

AFTER full ListTasks
Nested Loop Left Join (actual time=130.245..1274.721 rows=100000.00 loops=1)
  Buffers: shared hit=1222094
  -> Index Scan using tasks_pkey on tasks (actual time=0.012..23.016 rows=100000.00 loops=1)
       Buffers: shared hit=2094
  -> Aggregate (actual time=0.003..0.003 rows=1.00 loops=100000)
       Buffers: shared hit=220000
       -> Index Only Scan using events_task_activity_idx on events (actual time=0.001..0.001 rows=0.20 loops=100000)
            Index Cond: (task_id = tasks.id)
            Heap Fetches: 20000
            Buffers: shared hit=220000
  ...
  SubPlan 3
    -> Result (actual time=0.003..0.003 rows=1.00 loops=100000)
         Buffers: shared hit=400000
         InitPlan 2
           -> Limit (actual time=0.002..0.002 rows=1.00 loops=100000)
                Buffers: shared hit=400000
                -> Index Only Scan using events_task_last_activity on events e (actual time=0.002..0.002 rows=1.00 loops=100000)
                     Index Cond: (task_id = tasks.id)
                     Heap Fetches: 100000
                     Buffers: shared hit=400000
Execution Time: 1286.180 ms
```

Both omitted tag subtrees consume 600,000 hits: 300,000 through `task_tags_pkey`
and 300,000 through `tags_pkey`, with 100,000 one-row sorts (25 kB each). The
remaining 2,094 task, 220,000 count and 400,000 maximum hits reconcile exactly
to the root's 1,222,094 hits. Both 011 and 015 indexes are asserted by the test.

### Prepared generic logs: old optional OR filters versus selected predicates

On that same version-016 fixture, set `plan_cache_mode=force_generic_plan` and
PREPARE both SQL shapes. Old SQL is the historical optional-parameter OR query,
retained explicitly as a baseline in the test. New SQL comes from the real
`logQuery(event.Filter{TaskID:777, Limit:500, Desc:...})`, not a handwritten
replacement. Both are executed once to warm up, then explained. Generic plans
show `$1`/`$2` index predicates rather than substituted constants.

Other filters are unselected: after=0, until=0, agent empty, kinds empty, since
NULL. Task777 has two events, so the limit cannot hide a full scan. The test
compares **all returned event columns and their order** in ASC and DESC modes.
This measures SQL-shape improvement using the existing migration-001
`events_task_id_idx`, **not** benefit from any new index in 016.

| Generic task log | Execution ms | Root shared hits | Rows filtered | Access path |
| --- | ---: | ---: | ---: | --- |
| Old optional OR, ASC | 49.101 | 4,396 | 199,998 | events_pkey forward |
| New selected predicate, ASC | 0.040 | 5 | 0 | events_task_id_idx forward |
| Old optional OR, DESC | 45.666 | 4,396 | 199,998 | events_pkey backward |
| New selected predicate, DESC | 0.044 | 5 | 0 | events_task_id_idx backward |

Actual ASC plans:

```text
GENERIC LEGACY LOG ASC
Limit (cost=0.42..599.23 rows=4 width=114) (actual time=0.243..49.065 rows=2.00 loops=1)
  Buffers: shared hit=4396
  -> Index Scan using events_pkey on events (cost=0.42..5689.11 rows=38 width=114) (actual time=0.241..49.060 rows=2.00 loops=1)
       Index Cond: (id > $1)
       Filter: ((($6 IS NULL) OR (created_at >= $6)) AND (($2 = 0) OR (id <= $2)) AND (($3 = 0) OR (task_id = $3)) AND (($4 = ''::text) OR (agent_name = $4) OR (actor_agent = $4)) AND ((cardinality($5) = 0) OR (kind = ANY ($5))))
       Rows Removed by Filter: 199998
       Buffers: shared hit=4396
Planning Time: 0.034 ms
Execution Time: 49.101 ms

GENERIC SELECTED LOG ASC
Limit (cost=0.42..8.44 rows=1 width=114) (actual time=0.017..0.020 rows=2.00 loops=1)
  Buffers: shared hit=5
  -> Index Scan using events_task_id_idx on events (cost=0.42..8.44 rows=1 width=114) (actual time=0.016..0.018 rows=2.00 loops=1)
       Index Cond: ((task_id = $2) AND (id > $1))
       Buffers: shared hit=5
Planning Time: 0.013 ms
Execution Time: 0.040 ms
```

DESC retains the same predicates but uses `Index Scan Backward`. Old actual scan
`23.235..45.630`, new `0.018..0.021`, with execution totals 45.666 and 0.044 ms.
The selected-filter task-index assertion is intentionally about a forced generic
plan, which catches regressions that a literal-only EXPLAIN would miss. It does
not establish selectivity for global actor-only, agent-only or time-only filters,
so no unsupported actor/agent/time index was added from this test.

## Decisions and costs

* Add `task_tags(tag_id)`: the existing `(task_id,tag_id)` primary key already
  serves forward task lookup, but the reverse lookup scanned 100,000 rows.
* Add `memories(tag_id,id)` and `memories(agent_name,id)`: owner listing and
  wipe/FK lookup no longer scan the other 199,900 memories. Retain ID as the
  repository's ordered-list suffix, without promising that every plan uses an
  ordered scan. No body/created_at covering columns.
* Add `sessions(agent_name)`: the existing `(task_id,agent_name)` primary key
  remains appropriate for binding/session lookup. The missing leading agent
  key matters for agent-scoped listing and deletion. The saved plan measures
  the storage filter/order only, not the independent worker/pg_locks status
  subqueries. No claim about the empty-name all-agent listing or generic-plan
  parameter selectivity is made.
* Add `attachments(task_id,id)` for selective, ordered task listing and leading
  task FK support. Add `attachments(event_id)` from the measured event probe.
* Add the partial active jobs `(agent_name,coalesce(task_id,context_task_id),id)`
  index. Its predicate exactly matches `pending/running/archiving`. It does not
  filter by `available_at`: an unavailable earlier retry must still block a
  later job in the same session. `jobs_pending_idx` remains useful for global ID
  order and is not removed based on this single-agent fixture.
* Add full `jobs(task_id)`, `jobs(agent_name)`, `jobs(schedule_rule_id)` indexes:
  full FK probes include terminal history. Neither the active expression index
  nor the existing active schedule-rule unique index substitutes for those.
  `jobs(event_id,agent_name)` already has a unique constraint with event_id
  leading, so no redundant event FK index. `context_task_id` has no FK and is
  covered for the demonstrated queue expression, so no standalone index.
* Keep 011 `events_task_activity_idx` and 015 `events_task_last_activity` unchanged:
  both are already selected, at four buffer hits each. Existing
  `events_task_id_idx` supports task-scoped event pagination. No wider event
  covering index, extra activity index, or materialized counts in this migration.
* Defer `rules(source)` and `rules(target)`: at a deliberately substantial
  1,000-rule configuration the probes cost only 10-11 buffers and under 0.3 ms.
  The source fixture is NULL-only, so this is not proof about high-cardinality
  completed-source rules. Revisit if real manifests scale beyond this fixture.
  `rules.task_id` was removed in migration 004, so it is not a current FK candidate.
  `behaviour_manifest_state.active_id` is a singleton, with no evidence for an
  additional referencing index. Primary keys already lead the other task FKs.

Actual `pg_relation_size` bytes after the build:

| New index | Bytes |
| --- | ---: |
| task_tags_tag_idx | 704,512 |
| memories_tag_id_idx | 6,332,416 |
| memories_agent_id_idx | 7,143,424 |
| sessions_agent_idx | 761,856 |
| attachments_task_id_idx | 6,332,416 |
| attachments_event_idx | 4,513,792 |
| jobs_active_session_idx | 3,932,160 |
| jobs_task_idx | 1,867,776 |
| jobs_agent_idx | 1,400,832 |
| jobs_schedule_rule_idx | 1,392,640 |
| **Total** | **34,381,824** |

This is about 32.8 MiB additional storage for this fixture, plus WAL, write
amplification and concurrent-build I/O. The full memory indexes also store NULL
owners. Further partial/covering alternatives need their own workload evidence.
No INSERT/UPDATE throughput improvement is claimed.

## Online migration, interruption and retry

Goose `NO TRANSACTION` is mandatory for concurrent index DDL. Up uses
one `DROP INDEX CONCURRENTLY IF EXISTS` per index and one separate
`CREATE INDEX CONCURRENTLY` per build. Down only drops these indexes. Never put
these statements inside a DO block or explicit transaction. No
`CREATE INDEX IF NOT EXISTS`: a failed
concurrent build can reserve the name with `indisvalid=false`, causing that form
to silently leave queries unindexed.

Retry runs each drop/build pair again, including earlier successful pairs. This
is intentionally simple and robust: invalid leftovers and valid partial progress
are both recoverable without catalog writes or application-side SQL parsing.
It trades repeated I/O and temporary loss of that index's acceleration for
correctness. No constraint-backed index is dropped. The existing namespace
migration advisory lock serializes supported migration runners. Direct external
DDL still needs operator coordination.

Concurrent does not mean lock-free or bounded total runtime. Long transactions,
snapshots and conflicting DDL can delay a build, and the configured lock timeout
can fail it. After resolving the blocker, rerun `looster db migrate` or
`looster -N NAME db migrate`. Migration 016 is recorded only when all statements
succeed. There is no unbounded automatic retry loop or hidden lock-timeout
increase. If a build failed, do not manually mark version 016 applied.

### Lock bounds and migration-runner snapshot safety

Interactive/application connections receive a **5 second** `lock_timeout`
fallback. Namespace migration connections and Goose DDL receive a **30 second**
fallback, allowing a concurrent initializer to finish several index builds.
`namespaceConfigWithLockTimeout` installs the value after startup only when the
effective setting is zero. Nonzero explicit DSN, PGOPTIONS, role or database
settings remain authoritative. A caller's context can impose a shorter overall
bound. These are lock-wait bounds, not a total query or migration runtime limit,
and `statement_timeout` is not changed.

The migration mutex uses `pg_try_advisory_lock`, polling every **25 ms** until the
configured lock-timeout duration or caller cancellation. It does **not** sit
inside a blocking `pg_advisory_lock` call. A blocked advisory-lock statement can
hold a statement snapshot while the owning runner is performing a concurrent
expression-index build. That old snapshot can prevent the build from finishing,
while the waiter is itself waiting for the owner's migration lock. Polling
finishes each unsuccessful SQL statement and releases its snapshot before the
Go timer waits, avoiding that waiter/build interlock.

The dedicated migration connection keeps the successful session advisory lock
until migration completion and connection close. DDL conflicts still report
PostgreSQL lock timeout SQLSTATE `55P03`. Exhausting the polled namespace mutex
wait returns a context-deadline/cancellation error instead, with the
`namespace migration lock wait` diagnostic when its timer expires. Retry only
after resolving contention. The lock tests cover effective fallback/override
settings, bounded migration contention, and concurrent namespace initialization
with real concurrent index DDL.

The tests verify:

1. Fresh default installation and fresh non-default namespace install, checking
   all ten indexes for `indisvalid`, `indisready`, actual table/key definitions,
   and the exact active-state predicate.
2. Upgrade from version 015 in both public and a non-default namespace, preserving
   existing session rows and handling same-named valid partial-progress indexes.
3. A genuinely failed `CREATE UNIQUE INDEX CONCURRENTLY` on duplicate session
   agent names produces an invalid same-named index. No direct catalog mutation.
4. A held SHARE table lock plus a 50 ms migration lock timeout fails the real
   embedded migration at the session index with SQLSTATE `55P03`, after earlier
   index statements have committed. The version remains 015. Retrying repairs
   the invalid index and completes every remaining index. Another migration run
   is idempotent after completion.
5. A same-named public sentinel index remains attached to its public table during
   non-default retries. Search paths do not fall back to public.
6. Opt-in large-fixture plans assert the selected new index for each corresponding
   probe and retain the expected 011/015 access paths. Tests assert access paths,
   not machine-dependent sub-millisecond timing thresholds.
7. Rollback from 016 to 015 drops all ten indexes, then reapplication restores
   valid definitions, in both default and non-default namespaces.
8. The complete 100,000-task ListTasks plan retains both existing activity
   indexes before and after. Forced generic ASC/DESC old-versus-selected log
   plans use the real query builder and preserve exact returned rows/order.

Validation after the complete-query additions: `go test ./postgres -count=1`
passed with the disposable database, as did `go test -race ./postgres -run
'^TestQueryIndexes' -count=3`. Both opt-in large-fixture evidence tests above
passed individually. Timing values in this document come from the dated
non-race runs, not from race-instrumented tests.

The migration's Down section removes only these ten indexes concurrently. Older
irreversible data migrations are untouched. Apply migration 016 independently to
each existing namespace using the schema owner, not the application read/write
login. Use a disposable server when reproducing these fixture tests.
