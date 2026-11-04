# Running Looster

[Back to README](../readme.md) · [Installation](installation.md) ·
[Dashboard](dashboard.md) · [Namespaces](namespaces.md) ·
[Contributing](contributing.md)

Looster's CLI talks directly to PostgreSQL through one shared database login.
Human and agent permissions are enforced by the application.
`serve` consumes durable jobs for its configured agents; no HTTP listener is needed.
Multiple servers may share a database. They can handle different agents or compete
for the same agents. Connection-scoped advisory locks serialize each agent/task
pair. Task status and dispatch are committed together through database triggers.

## Local setup

Install a [release binary or build from source](installation.md). Only source
builds require Go (use the version in `go.mod`); sqlc and Goose CLI tools are not
runtime prerequisites. Install Docker with Compose and authenticate Jcode
separately. Jcode is an external runtime, not a Go build dependency. The commands
below use a source-built `bin/looster`; substitute `looster` for an installed
release binary. From the source checkout:

```bash
go build -buildvcs=false -o bin/looster ./cmd/looster
docker compose up -d --wait
LOOSTER_DATABASE_URL='postgres://postgres:postgres@localhost:5432/looster?sslmode=disable' bin/looster db migrate
cp config.example.toml config.toml
bin/looster --config config.toml behaviour apply -f behaviour_example.txt
bin/looster --config config.toml serve
```

In another shell:

```bash
# project/example is declared in the applied manifest.
printf 'Build a settings page\n' | bin/looster --config config.toml task create 'Settings page' -t project/example --status planning
bin/looster --config config.toml status
bin/looster --config config.toml task ps
bin/looster --config config.toml task follow TSK-1
```

`--config` defaults to `$XDG_CONFIG_HOME/looster/config.toml` or
`~/.config/looster/config.toml`. A missing default file is fine when using env vars.
The shorthand `looster path/to/config.toml` starts `serve` with that config.

## Namespaces and upgrades

All commands support `--namespace NAME` / `-N NAME`, with `LOOSTER_NAMESPACE`
and TOML `namespace` as defaults. Existing databases belong to `default`, mapped
to their existing `public` schema without changing IDs or moving rows.

```bash
bin/looster namespace create project-a
bin/looster namespace list
bin/looster -N project-a behaviour apply -f behaviour_example.txt
bin/looster -N project-a serve
bin/looster -N project-a task ls
bin/looster -N project-a db migrate
```

Namespaces have separate schemas, migration histories, tasks, configuration,
queues, workers, sessions, and logs. Non-default namespaces also separate managed
workdirs and object prefixes. Child harnesses inherit `LOOSTER_NAMESPACE`.
The dashboard switcher next to the logo changes the namespace of reads and writes.
Schema isolation does not replace database roles, object-store grants, or harness
sandboxing. Use separate credentials or databases for hard security boundaries.

For upgrades, back up the database and stop old workers before rebuilding and
running `db migrate` in each namespace. Existing agent/tag definitions and rules
become a generated initial manifest because their original file was not stored.
New versions retain exact source. Tasks previously `unvalidated` become `todo`,
not automatically validated. Rules targeting that removed status map to `todo`
(and `stop_unvalidated` to `stop_todo`). Historical events, sessions, memories,
attachments, and existing queued jobs remain intact. Migration does not replay
status events. Review the mapped rules and apply the intended manifest before
restarting workers. Reversing these migrations requires restoring a backup
rather than guessing which work used to be in the removed state.

## System logs

`looster logs` (alias `looster log`) shows the latest 100 matching database events
in chronological order with readable timestamps, event IDs, kinds, agent/actor,
task references where present, and message bodies. Jcode text events are decoded
for readability. Add `--json` for the original event records as NDJSON.

```bash
bin/looster logs
bin/looster logs -f
bin/looster logs -f --agent coder
bin/looster logs --task TSK-1 --since 2h
bin/looster logs --kind worker_start_failed,worker_error,scheduler_failed,agent_failed
bin/looster logs --since 2026-10-29T10:00:00Z --json
bin/looster logs --tail -1 --kind worker_started,worker_stopped
bin/looster logs -f --tail 0 --json
bin/looster logs --after 120 --json
```

`--agent` matches both events for that agent and changes it authored. `--kind`
accepts comma-separated kinds or repeated flags. Filters are applied in PostgreSQL
before tailing, including to standalone schedules with no task. `--since` accepts
a positive duration such as `10m`/`2h`, or an RFC3339 timestamp. `--tail` accepts
0 through 10000, or -1 for all history. `--after` replays every matching event
after an ID, across pages; use it instead of `--tail`. Follow mode polls every
second by default; change that with `--interval`.

Worker lifecycle and scheduler/queue errors are persisted alongside task and
agent events, so logs combine activity across all servers sharing the database.
Events include `worker_started`, `worker_stopped`, `worker_start_failed`,
`worker_error`, `scheduler_dispatched`, `scheduler_failed`, `scheduled`,
`agent_started`, `agent_output`, `agent_stopped`, `agent_failed`, and
`notification_handled`. An empty result prints a notice to stderr; follow mode
keeps waiting. Ctrl-C exits cleanly. `task follow` remains available with its
existing NDJSON output.

`serve` also writes operational messages to stderr. When PostgreSQL is unreachable,
or failure happens before a connection is available, the error cannot be persisted
to the remote event table; inspect the terminal or service manager output for
that process. Existing `looster status` reports currently live worker heartbeats.
No additional database migration is required for the log commands.

## Agent skills

Generate the bundled agent skill into the default skills directory or a custom
skills root:

```bash
bin/looster skills generate
bin/looster skills generate ./project-skills
```

The default output is `~/.agents/skills/looster/SKILL.md`; the custom example writes
`./project-skills/looster/SKILL.md`. This command needs no config or connection to
PostgreSQL or S3, and embeds no credentials. Its instructions explain the agent
workflow, permissions, task statuses, memories, attachments and sessions.
Identical content is left alone; use `--force` to replace a skill with local edits.

## Environment configuration

Copy `.env.example` to `.env` and edit the PostgreSQL, S3, and worker values.
`mise.toml` uses its native `[env]` directive to load that file; no shell script
is needed. `.env` is gitignored and its values are marked for redaction in mise.

```bash
cp .env.example .env
mise exec -- bin/looster task ls
```

The same values are available in a shell with mise activated. A missing `.env`
is fine when your process environment is already configured. Looster itself does
not parse `.env` files. Database URLs and S3 credential keys are environment-only;
`database_url`, `s3.access_key`, `s3.secret_key`, and `s3.session_token` in TOML
are rejected, even when an environment value would override them. Move existing
values into the environment and remove those keys from old TOML files.
The CLI, server and migrations use the same database login; migrations require
ownership of the Looster schema.

For non-secret settings, precedence is command flags, environment variables,
TOML, then defaults. Credentials have no TOML or command-line source. Set
`LOOSTER_DATABASE_URL` or the native PG variables in the environment instead of
passing a `--database-url` flag. PostgreSQL and S3 are configurable without a file:

| Variable | Meaning |
| --- | --- |
| `LOOSTER_DATABASE_URL` | PostgreSQL URL, including credentials, TLS and pgx pool options |
| `LOOSTER_S3_ENDPOINT` | S3-compatible endpoint; omit for AWS S3 |
| `LOOSTER_S3_REGION` | Signing region, default `us-east-1` |
| `LOOSTER_S3_BUCKET` | Existing bucket name |
| `LOOSTER_S3_ACCESS_KEY` | Optional explicit access key |
| `LOOSTER_S3_SECRET_KEY` | Explicit environment secret key; supply both keys together |
| `LOOSTER_S3_SESSION_TOKEN` | Optional session token for temporary credentials |
| `LOOSTER_AGENTS` | Comma-separated agent names, e.g. `designer,coder` |
| `LOOSTER_ROLE` | Application role: `human` (default) or `agent` |
| `LOOSTER_AGENT` | Agent identity, required in agent mode |
| `LOOSTER_CONCURRENCY` | Parallel jobs per server, default `4`, maximum `128` |
| `LOOSTER_WORKDIR` | Workdir root, default `./workdirs` |
| `LOOSTER_POLL_INTERVAL` | Job polling duration, default `1s` |
| `LOOSTER_RUN_TIMEOUT` | Harness run timeout, default `1h`; `0s` disables |
| `LOOSTER_JCODE_BINARY` | Jcode executable, default `jcode` |
| `LOOSTER_JCODE_SESSIONS_DIR` | Saved history directory, default `~/.jcode/sessions` |

Use a complete `LOOSTER_S3_ACCESS_KEY` / `LOOSTER_S3_SECRET_KEY` pair, optionally
with `LOOSTER_S3_SESSION_TOKEN`. Alternatively supply `AWS_ACCESS_KEY_ID`,
`AWS_SECRET_ACCESS_KEY`, and optional `AWS_SESSION_TOKEN` in the environment.
Shared AWS credential files and profiles are not credential sources. Partial key
pairs fail with a key-only error. Looster uses path-style S3 requests for compatibility.

Example using only environment variables:

```bash
export LOOSTER_DATABASE_URL='postgres://postgres:postgres@localhost:5432/looster?sslmode=disable'
export LOOSTER_S3_ENDPOINT='http://localhost:4566'
export LOOSTER_S3_REGION='us-east-1'
export LOOSTER_S3_BUCKET='looster'
export LOOSTER_S3_ACCESS_KEY='test'
export LOOSTER_S3_SECRET_KEY='test'
bin/looster task ls
```

### PostgreSQL environment variables

A connection string is optional. Without one, normal commands and `db migrate`
use PostgreSQL environment settings and the driver's defaults:

```bash
export PGHOST='localhost'
export PGPORT='5432'
export PGUSER='postgres'
export PGPASSWORD='postgres'
export PGDATABASE='looster'
export PGSSLMODE='disable'
bin/looster task ls
```

The native names follow [PostgreSQL's environment variable convention](https://www.postgresql.org/docs/current/libpq-envars.html).
Looster additionally accepts these underscore aliases:

| Native variable | Accepted aliases |
| --- | --- |
| `PGHOST` | `PG_HOST` |
| `PGPORT` | `PG_PORT` |
| `PGUSER` | `PG_USER` |
| `PGPASSWORD` | `PG_PASS`, `PG_PASSWORD` |
| `PGDATABASE` | `PG_DATABASE`, `PG_DB` |
| `PGSSLMODE` | `PG_SSLMODE`, `PG_SSL_MODE` |
| `PGSSLCERT` | `PG_SSLCERT` |
| `PGSSLKEY` | `PG_SSLKEY` |
| `PGSSLROOTCERT` | `PG_SSLROOTCERT` |
| `PGSSLPASSWORD` | `PG_SSLPASSWORD` |
| `PGCONNECT_TIMEOUT` | `PG_CONNECT_TIMEOUT` |
| `PGAPPNAME` | `PG_APPNAME` |

Nonempty native names win over their aliases. If multiple aliases are set, the
first one listed wins. Explicit connection-string fields override PG environment
defaults, including aliases; omitted fields still inherit environment values.
This applies to URL and PostgreSQL keyword/value connection strings. For example,
`postgres://postgres@localhost/looster` can obtain its password from
`PGPASSWORD` or `PG_PASS`. Password values are escaped without treating their
contents as connection parameters.

Set `LOOSTER_DATABASE_URL` or PG variables to configure the connection. Secret
TOML keys and the former `--database-url` flag are not supported. Passwords must
come from environment values, not `.pgpass` or service files. TLS certificate
and key paths may be supplied through environment variables. For migrations,
use the owner login, for example
`PGUSER=postgres PGPASSWORD=postgres bin/looster db migrate`.

The server forwards the shared database settings to harnesses and sets
`LOOSTER_ROLE=agent` with the correct `LOOSTER_AGENT` identity. Separate database
users or per-agent connection URLs are not needed.

## Tasks, memories and files

Task statuses are exactly `planning`, `todo`, `validated`, and `closed`.
Implementation awaiting independent review stays in `todo`, with evidence in a
comment. `AFTER coder PING reviewer` dispatches review on coding completion
without a separate task status. Mark work `validated` only after required
validation. Closing a task stops active runs and prevents queued task jobs from
starting while it remains closed. Session states (`running`, `idle`, `failed`)
are not task statuses.

Task IDs are `TSK-n`; replace example IDs with those returned by your commands.
Tags have a numeric `TAG-n` identity but are addressed by name in commands.
Memories use `MEM-n`. Detail/list output is JSON; `task follow` emits NDJSON.
Descriptions, comments and memory bodies come from stdin, limited to 8 MiB.
An interactive terminal without redirected input supplies an empty description.

```bash
looster task status TSK-1 todo
looster task status TSK-1 closed
printf 'Please review accessibility\n' | looster task comment TSK-1
printf 'Use semantic HTML\n' | looster tag memory add design/frontend
looster tag memory list design/frontend
looster tag memory rm design/frontend MEM-1
printf 'Replacement context\n' | looster agent memory wipe coder
looster agent memory wipe coder </dev/null  # clear all memories
looster task bind coder session_existing --task TSK-1
looster agent sessions coder
looster task file add TSK-1 ./spec.pdf
looster task file add TSK-1 ./review.txt --comment 12
looster task file list TSK-1
looster task file get TSK-1 1 ./download.pdf
looster task follow TSK-1 --after 20 --once
looster task ps --once
looster task ps --once --format json
```

`task ps` defaults to an ASCII table with task IDs, task statuses, names, agents,
session states, and session IDs. Tasks without sessions are included. Long cells
are shortened with `...`; `--format json` preserves the full snapshot. Without
`--once`, the table refreshes every second (`--interval` changes this), while JSON
snapshots are printed consecutively without terminal clear sequences.

Downloads refuse to overwrite existing files; `-` writes the bytes to stdout.
Comment attachment IDs refer to the event ID returned by `task comment`.
`task bind` accepts `LOOSTER_TASK_ID` instead of `--task` inside a harness.
Wipe replaces all memories atomically; empty stdin clears them.

## Behaviour and sessions

```text
ON TODO PING designer
AFTER designer COMPLETED PING coder
EVERY 10 MINUTES PING coder WITH "Review open tasks and report blockers."
```

Rules are case-sensitive and may have `--` comments. `behaviour check` validates
stdin or a file without connecting to a database. `behaviour apply` stores the
entire original file as an immutable version and atomically replaces the active
configuration. `create` and `set` are aliases for this same full replacement,
not incremental updates. Empty input clears the configuration. Rules apply to
future events. Transitioning
to `todo` or creating a task in `todo` dispatches jobs. Reassigning the same status
does not. Completion dispatches on the same task, and does not change task status.
Cycles are allowed and will keep dispatching; configure them deliberately.

### Batches from stdin or files

```bash
bin/looster behaviour apply --file behaviour_example.txt
bin/looster behaviour apply ./my-rules.looster
bin/looster behaviour apply < ./my-rules.looster
bin/looster behaviour check -f ./my-rules.looster
bin/looster behaviour list
bin/looster behaviour activate 1
```

`check` and `apply` accept a positional file or `--file`/`-f`; omit the file or
use `-` for stdin. Files and stdin are limited to 8 MiB. Statements can span lines
and are separated by semicolons; legacy one-rule-per-line input also works:

```text
ON TODO PING designer;
AFTER designer COMPLETED PING coder;
EVERY 2 HOURS
    PING coder WITH "Review progress; create follow-up tasks as needed.";
```

Semicolons and `--` inside quoted prompts are preserved. Comments end at a newline.
The entire file is parsed before a single transactional write; malformed input
never partially applies. Unchanged rules keep their schedule cursors. Every
successful application creates a version, retaining comments and whitespace.
Version activation restores a complete earlier configuration without modifying
its stored source. The dashboard displays the full file with a version switcher.
Empty input intentionally clears all active rules and definitions.

### Agent definitions and status rules

```text
AGENT planner WITH "Investigate tasks; set them to todo when ready.";
AGENT coder WITH "Implement tasks, test the result and report evidence.";
AGENT reviewer WITH "Review the implementation against the concrete task.";
ON PLANNING PING planner;
ON TODO PING coder;
AFTER coder PING reviewer;
```

`AGENT` creates or updates the named agent's instructions. An empty quoted
instruction string explicitly preserves an agent identity with no extra prompt,
including legacy agents that were registered without descriptions. Definitions are
applied before rules in the same transaction, preserving identity, sessions,
and memories. Identical declarations are deduplicated and conflicting ones are
rejected. Omitted instructions are no longer active after replacement.

Tags can only be defined through manifest statements, for example:

```text
TAG src/linear WITH "Use Linear only if this server has access.";
TAG org/looster WITH "Looster project work.";
ON PLANNING WHEN TAGGED src/linear PING planner;
AFTER coder WHEN TAGGED org/* PING reviewer;
AFTER coder WHEN NOT TAGGED org/* PING planner;
```

`WHEN [NOT] TAGGED` is available on `ON` and `AFTER` rules, including stop rules.
Patterns match whole names, with `*` matching any sequence including `/`.
Only task tags declared in the active manifest count as matches. A negative
condition succeeds when none match. Omitted legacy tags remain readable and
usable by existing tasks, but do not add instructions or block context loading.
Their identities, assignments, and memories remain available. `tag create`
and `tag rm` are no longer supported configuration operations.

`behaviour list` returns immutable versions with their full source and active
state. `behaviour activate ID` selects a previous version in the current namespace.

`ON` accepts PLANNING, TODO, VALIDATED, or CLOSED. It fires when a task is
created in that state or enters it later; setting the same state again does not
fire. `AFTER VERIFIED STOP PINGING coder` suppresses all task pings to `coder`
once the task reaches `validated`, including pending jobs, session notifications,
and completed handoffs. `VERIFIED` aliases `VALIDATED`. The same stop rule accepts
other task statuses, such as `AFTER TODO STOP PINGING planner`. It remains active
if the task later moves to another status, until the rule is removed. Agents
without a matching stop rule continue receiving pings.
`AFTER coder PING reviewer` and
`AFTER coder COMPLETED PING reviewer` are equivalent. The target receives the
triggering task's current ID, description,
status, tags, relevant memories, comments, attachments and latest reported peer
results. The handoff also names the source agent and its completed session ID.
The target's own agent/task session is started or resumed; the source session ID
is context, not the target's resume ID. Task context is loaded when the job runs.

The supplied `behaviour.txt` works directly with `behaviour check -f behaviour.txt`
and `behaviour apply -f behaviour.txt`. Configure a running server for the
agents it should execute using `LOOSTER_AGENTS`. The file declares `src/linear`
before tasks use it. Apply `looster db migrate` after upgrading.

### Recurring prompts

```bash
printf '%s\n' 'AGENT triage WITH "Inspect open work."; EVERY 10 MINUTES PING triage WITH "Report blockers.";' | bin/looster behaviour check
# Apply a complete file, including every definition and rule to retain.
bin/looster behaviour apply -f behaviour_example.txt
bin/looster behaviour list
```

The target agent must exist or be defined with AGENT in the same batch, and a server must have it in
its configured `agents` list. Upgrade the schema with `looster db migrate` before
using the scheduler.

Intervals are positive whole numbers in `SECOND(S)`, `MINUTE(S)`, `HOUR(S)` or
`DAY(S)`. Prompts are double-quoted JSON strings, so escaped quotes, newlines and
Unicode escapes work. `--` inside a prompt is preserved; outside quotes, a
whitespace-separated `--` starts a comment.

The first run is due one interval after the rule is saved. A server checks due
rules at `poll_interval`, even while its worker slots are busy. PostgreSQL row
locks commit each dispatch and its next due time atomically, so competing servers
do not dispatch the same occurrence twice. Schedules keep a fixed cadence, use
the database clock, and survive server restarts. After downtime, at most one run
is dispatched; the cursor advances past any additional missed ticks. If that
rule already has a pending/running/archiving job, its tick is skipped.

Each occurrence starts a fresh Jcode session with its configured agent instructions
and the supplied scheduled prompt. No task context or `LOOSTER_TASK_ID` is
injected. The default namespace keeps its historical no-workdir behavior;
non-default namespaces use isolated scheduled workdirs under their namespace.
The agent can inspect, triage, import or create tasks as its prompt
requires. These standalone runs do not fire task-based ON TODO or AFTER rules.
Retries of the same job can resume its captured session ID; separate scheduled
occurrences never share a session.

Prompts are snapshotted into jobs. `looster task follow` without a task ID shows
their events, which have no `task_id`. Their archives are stored at
`sessions/AGENT/scheduled/job-JOB_ID/SESSION_ID.json` in the default namespace;
other namespaces prepend `namespaces/NAME/`. `agent sessions` lists the
task-bound sessions; standalone runs are tracked in the job/event log.
Changing or removing a rule stops future ticks but leaves existing jobs and
history intact. Reapplying unchanged rules keeps their next due time.

### Manual agent pings

Mention an exact, case-sensitive registered agent name in a dashboard or CLI
comment to queue work:

```sh
printf 'Please @coder review this with @reviewer.\n' | looster task comment TSK-1
```

Only human comments trigger manual pings. Unknown names are ignored, repeated
mentions queue one job per agent per comment, and email addresses are not
mentions. Existing task sessions are reused without a second notification for
that comment. Closed tasks and applicable `STOP PINGING` rules block manual pings.

The Behaviour page gives each active `EVERY` rule its own **Run now** button and
rule ID. CLI users can discover the same active IDs and run a particular rule:

```sh
looster behaviour rules
looster behaviour run 42
```

These operations are human-only and namespace-scoped. Results distinguish
`queued` from `already_active` when the rule has a pending, running, or archiving
job. Manual runs use the configured prompt and standalone queue without changing
the next scheduled time or creating a manifest version. IDs remain stable while
definitions are unchanged. Use IDs from the current namespace's active rules,
not immutable manifest history or another namespace.

Each agent/task pair has one harness session. Jcode runs headlessly with
`--ndjson`, and an existing ID is passed with `--resume`. Workdirs are named
`AGENT-TSK-n`. Stream events are appended to the task log, and the full saved Jcode
session JSON is staged durably in PostgreSQL then uploaded to
`sessions/AGENT/TSK-n/SESSION_ID.json`. Repeated runs overwrite that session's
archive. A completion event is emitted only after the archive succeeds.
When resuming on another machine, a missing local session is restored from its
S3 archive. Keep the Jcode version compatible across servers. A first run that
crashes before any archive exists needs recovery on the machine with its saved
Jcode session.

S3 failures retry after ten seconds using the staged history, without rerunning
the harness. Interrupted runs are requeued; a crashed worker releases its database
lock so another worker can reclaim the job. Harness failures are recorded as
`agent_failed`; to retry, fix the cause and transition the task out of and back
into `todo`. Recovery has at-least-once execution semantics: an external harness
operation and a database commit cannot be atomic.

To continue after stopping `serve` (including Ctrl-C or SIGTERM), start it again
with the same database, configured agents, and workdir. Interrupted jobs are
automatically retried after ten seconds with their captured session IDs; no task
status change is needed. Keep the local Jcode sessions directory and workdirs
on persistent storage. An abrupt crash can only resume from the latest history
Jcode saved, and unfinished history is not uploaded to S3. For recovery of an
unfinished first run, restart on the same machine. A configured run timeout is
still treated as a harness failure rather than a server shutdown.

### Task change notifications

Content edits, status changes, comments, tag changes and file changes enqueue a
notification for each existing session attached to that task, excluding the
agent that made the change. Human changes notify all attached sessions. The
origin is carried through application writes and stored as `actor_agent` on the
event; transaction-local attribution is cleared when the transaction ends.

Each notification resumes the existing session with a change message and current
task context. A busy session receives it after its current run finishes, using the
same lock that prevents concurrent resumes. Workflow rules and notifications
targeting the same agent for the same event dispatch only one job.

Deletion sends a final stop-work message to other sessions with captured harness
IDs before their task/session rows are removed. The queued message retains its
session ID and original archive/workdir reference without recreating the task.
Streamed output and lifecycle events do not fan out. Completing a notification
records `notification_handled` and does not fire AFTER COMPLETED rules. Agents
should act only on actionable changes, not post acknowledgement-only comments.

Run `looster db migrate` after upgrading to install standalone schedules and
notification triggers. These forward migrations do not have a lossless rollback
for standalone history; a rollback requires a database backup restore.

## Application roles and database setup

Use one database login for all Looster processes. Run `looster db migrate` as the
schema owner; it does not need `CREATEROLE` and does not create logins or passwords.
Configure that login through the process environment, optionally loaded from a
trusted local `.env` file by your shell or mise. Never put it in Looster's TOML.
Compose's `postgres` login
and password are local development defaults.

`human` is the default application role and can use all commands. Agent mode is
set automatically for managed Jcode processes. To run an agent CLI manually:

```bash
LOOSTER_ROLE=agent LOOSTER_AGENT=coder bin/looster task ls
bin/looster --role agent --agent-name coder agent memory list coder
```

The application repository enforces these permissions:

| Operation | Human | Agent |
| --- | --- | --- |
| Read tasks, tags, agents, memories and logs | Yes | Yes |
| Create/update/remove tasks, comment, attach files | Yes | Yes |
| Modify tag memories | Yes | Yes |
| Modify agent memories and bind sessions | Any agent | Own identity only |
| Create/remove agents and tags, change behaviour | Yes | No |
| Run the server or migrations | Yes | No |

Roles are local application settings, not authenticated user accounts. Processes
with direct database access or control of their own environment are trusted;
these checks do not sandbox a hostile agent. Event immutability and coordination
remain database invariants.

When upgrading an existing installation, run `looster db migrate` using the
existing schema owner. The upgrade removes the old RLS policies and database-role
mapping while preserving tasks, memories and sessions. Existing database logins
are left intact, since they may be used elsewhere. Remove obsolete per-agent
connection URLs and use the shared login in your config. Existing PostgreSQL
volumes keep their existing credentials; changing an example file does not rotate
a live database password.

Configure the pool with at least `concurrency + 4` connections. Use direct
PostgreSQL connections or session pooling: transaction pooling cannot preserve
the connection-scoped locks that coordinate sessions.

## Development

```bash
sqlc generate
go test ./...
go test -race ./...
go vet ./...
go build -buildvcs=false -o bin/looster ./cmd/looster
```

Domain types live in `task`, `agent`, `tag`, `memory`, `event`, `namespace` and `behaviour`.
`app` owns application ports and orchestration. `postgres`, `jcode` and
`objectstore` implement those ports; `cli` wires them together. Goose migrations
are embedded, and sqlc query sources plus generated bindings are committed.
Unit tests use fake ports and a local fake Jcode executable; they do not need
PostgreSQL, S3 or a paid model session. Infrastructure and authenticated Jcode
should be exercised manually with the Compose setup.
