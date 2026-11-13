<p align="center"><img src="logo.svg" width="160" height="160" alt="Looster logo"></p>

# Looster

**Durable workflows for teams of coding agents.**

## Why Looster

Spin up agents across multiple machines without public worker IPs, VPNs, or mesh
networking. Looster uses **shared, reachable PostgreSQL as its control plane**:
each machine needs database connectivity, not inbound connections from its peers.
Workers also need access to their configured Jcode runtime, project tools, and
S3-compatible storage for files and session archives. This does not make the
database safe to expose without proper credentials, TLS, and network controls.

Run agents on separate VMs or physical machines, each with its own harness
installation and authentication, tools, and skills. **Jcode is the currently
implemented harness adapter**; each host can provide the project access and
capabilities its assigned agents need.

Compose behaviour through agent prompts, tag prompts, shared memories, tasks,
and comments. Agent instructions define roles, tag instructions carry project
context, and task descriptions and comments keep concrete work and review evidence
together. Declare handoffs once, then follow durable tasks and resumable sessions
from the CLI or dashboard. Namespaces keep independent projects separate, while
machines can run different agents or share the workload.

Every agent run receives a required ASD-STE100 Simplified Technical English
instruction. This includes scheduled runs, task runs, retries, and resumed
sessions. The rule covers all text that agents write, including tasks, comments,
logs, code comments, commits, PRs, release notes, and other artifacts. Agents must
pass the rule to other agents. Code, exact technical values, quotations, and raw
tool output stay unchanged. This is a prompt requirement, not an automatic
language checker or a guarantee of formal ASD-STE100 compliance.

## Architecture

```mermaid
flowchart BT
    PG[(PostgreSQL control plane)]
    CLI[CLI clients] --> PG
    WEB[Local web UI] --> PG
    SERVERA[Looster server A] --> PG
    SERVERB[Looster server B] --> PG
    HARNESSA[Jcode harnesses on machine A] --> SERVERA
    HARNESSB[Jcode harnesses on machine B] --> SERVERB
```

Servers consume durable jobs and coordinate agent/task sessions through PostgreSQL.
They launch local Jcode harnesses and collect their output; the harness arrows
show this local integration, not public network listeners. CLI and web processes
connect directly to the same database. DuckLake on S3-compatible storage holds
heavyweight payloads, while S3 also holds attachments and session archives.
No worker-to-worker mesh is needed. See
[architecture](docs/architecture.md) and [session recovery](docs/usage.md#behaviour-and-sessions).

## Source layout

- `cmd/looster/` is the executable entry point. Build it with `go build ./cmd/looster`.
- `internal/` contains private application packages, including the CLI, domain
  model, worker orchestration, PostgreSQL storage, dashboard, and bundled skills.
- Database migrations, SQL queries, and generated database code live together in
  `internal/postgres/`. `sqlc.yaml` targets these paths.
- Dashboard assets live in `internal/web/static/` and agent skill instructions in
  `internal/skills/looster/`. Both remain embedded in the executable.
- `scripts/` contains the distributable installer. Its Go tests live in
  `internal/scripts/`.

Run `go test ./...` and `go vet ./...` from the repository root. The module path
is `github.com/api-mittens/looster`, and the build target is `cmd/looster`.
Release builds inject the version
through `github.com/api-mittens/looster/internal/cli.Version`.

## Install

Release binaries support **Linux and macOS, amd64 and arm64**, and need **no Go
toolchain or separate DuckDB executable**. Windows support has been removed.
Download the installer to a file, inspect it, then run it:

```sh
curl -fsSL https://raw.githubusercontent.com/api-mittens/looster/master/scripts/install.sh -o install-looster.sh
# Review install-looster.sh before running it.
sh install-looster.sh
export PATH="$HOME/.local/bin:$PATH"
looster --help
```

The installer detects your platform, verifies the release archive's SHA-256
checksum, and installs to `~/.local/bin` without root. Set `LOOSTER_VERSION` to a **v-prefixed**
release tag such as `v0.1.0`, `LOOSTER_INSTALL_DIR` to change the destination, or
`LOOSTER_REPO` to override `api-mittens/looster`. Omit the version for the latest
release. See [installation](docs/installation.md) for manual downloads and source
builds using the Go version declared in `go.mod`.

Alternatively, use [mise](docs/installation.md#install-with-mise-alternative) to
install the Go-based tool globally or declare it in your repository's `mise.toml`:

```sh
mise use go@1.27.2
mise use go:github.com/api-mittens/looster/cmd/looster@latest
mise exec -- looster --help
```

This alternative builds from source and requires Go, CGO and a C/C++ toolchain. The installation guide covers
shared config, private `mise.local.toml` overrides, version pinning, and shell setup.

## Automatic DuckLake storage

Application commands require PostgreSQL and S3-compatible storage and initialize
DuckLake automatically using the built-in DuckDB engine. PostgreSQL retains the
control plane and payload references, while DuckLake stores heavyweight payloads
under an isolated namespace catalog schema and S3 prefix. No separate DuckDB
executable or engine download is needed. Signed extensions download on first use.
Human-only `looster configure storage` provisions missing databases and applies
migrations. Its `--ducklake` option prewarms storage, not runtime opt-in.
See [storage configuration](docs/storage.md) for credentials, namespace isolation
and disposable verification, and [historical migration](docs/storage-migration.md)
for the coordinated upgrade, guarded backfill and cleanup safety gate.
The existing `looster db migrate` command remains migration-only.

## Quick start

Install and authenticate [Jcode](https://github.com/1jehuang/jcode) separately. Install Docker with Compose for local
PostgreSQL and S3-compatible infrastructure. Clone the repository (or use an
existing checkout), then start the local services:

```sh
git clone https://github.com/api-mittens/looster.git
cd looster
docker compose up -d --wait
cp config.example.toml config.toml
cp .env.example .env
# Review/edit .env, then export its trusted contents in this shell.
set -a
. ./.env
set +a
looster --config config.toml db migrate
looster --config config.toml behaviour check -f behaviour_example.txt
looster --config config.toml behaviour apply -f behaviour_example.txt
looster --config config.toml serve
```

The generic [behaviour_example.txt](behaviour_example.txt) and example config run
`designer`, `coder`, and `reviewer`. Adapt their instructions to the repository
and tools available on your machines. Apply the complete manifest before starting
workers. In another terminal, create a planning task:

```sh
printf 'Build a settings page\n' | looster --config config.toml task create 'Settings page' -t project/example --status planning
looster --config config.toml task ls
looster --config config.toml logs -f
```

The designer plans the work and moves it to `todo`; the coder implements it, and
the reviewer validates the result. Use the returned task ID with
`looster --config config.toml task follow TSK-1` to follow its events.
The Compose credentials are local development defaults, not production settings.
Load the same environment in every terminal running Looster. Database connection
strings and S3 keys are environment-only; secret-bearing TOML keys are rejected.

To spread work across machines, configure the same reachable PostgreSQL and
S3-compatible endpoints on each host, and provide credentials through that
host's environment. The example `localhost`
endpoints work only on the machine running Compose. Install and authenticate
Jcode separately on every worker host, copy the appropriate config, then select
which agents each server handles:

```sh
# Host A
LOOSTER_AGENTS=designer,coder looster --config config.toml serve
# Host B
LOOSTER_AGENTS=reviewer looster --config config.toml serve
```

Both servers use the same namespace and applied manifest; they do not need
public worker addresses or direct connections to one another.

## Behaviour: declare your workflow

Behaviour manifests define agent instructions, shared tag context, and dispatch:

```text
AGENT designer WITH "Plan the work and move ready tasks to todo.";
AGENT coder WITH "Implement the task and record test evidence.";
AGENT reviewer WITH "Review independently before validating.";
TAG project/example WITH "Shared project context.";
ON PLANNING PING designer;
ON TODO PING coder;
AFTER coder PING reviewer;
AFTER VALIDATED STOP PINGING coder;
```

`ON` reacts to task states, `AFTER` hands off completed runs or stops pings, and
`EVERY` schedules recurring prompts. Tag conditions can route particular work.
The example above illustrates the syntax; use the complete supplied manifest for
the quick start. Applying a manifest atomically replaces the active configuration
and stores its exact source as an immutable version. It is **not an incremental
patch**. See [behaviour and sessions](docs/usage.md#behaviour-and-sessions).

## Web UI

> [!CAUTION]
> **LOCAL MACHINE ONLY. NO AUTHENTICATION OR SECURITY BOUNDARY. NEVER EXPOSE PUBLICLY.**
> Run the web UI on your local machine, pointing at your working database. Keep
> the default loopback binding (`127.0.0.1:8080`). Do not publish it on a public
> interface, port forward it, or treat namespaces as access control. The UI can
> read and change the database using its configured credentials.

```sh
looster --config config.toml web
```

Open `http://127.0.0.1:8080` locally. The graph connects tasks to agents
with real sessions, with animated links for active work. Planning and todo tasks
are shown by default. Use the individual status checkboxes to include validated
or closed tasks, or hide any status. Status choices combine with tag and search
filters and survive refreshes and page navigation, resetting when you switch
namespaces. Click nodes for details and filter by tags to explore the workflow.

![Looster graph dashboard showing example tasks and agent relationships](docs/images/graph.png)

*Actual dashboard screenshot populated with synthetic demo data, not production work.*

### Use the heatmap to optimize work

Toggle the heatmap to spot tasks with high comment and state-change activity.
Use those signals to investigate repeated handoffs, clarification churn, or work
that would benefit from better shared tag context. Compare agent links and task
history before changing your manifest. Heat is **activity, not errors, task
health, elapsed effort, or a performance score**. Quiet tasks shrink as their last
activity ages, while running tasks stay full-sized. This helps surface where to
look, but does not establish which agent or workflow is inefficient. See the
[dashboard guide](docs/dashboard.md) for counters, filters, layout, and aging.

## Documentation

| Guide | What you will find |
| --- | --- |
| [Installation](docs/installation.md) | Installer options, release archives, source builds |
| [Usage and configuration](docs/usage.md) | CLI, credentials, tasks, sessions, manifests, recovery, upgrades |
| [Dashboard](docs/dashboard.md) | Graph, filters, navigation, activity and layout |
| [OpenTelemetry](docs/observability.md) | Optional traces, metrics, logs, and safe export settings |
| [Namespaces](docs/namespaces.md) | Project isolation and database boundaries |
| [Architecture](docs/architecture.md) | Package boundaries and application permissions |
| [Database performance](docs/database-performance.md) | Query-plan evidence, indexes, bounded locks and online migration retries |
| [Contributing](docs/contributing.md) | Go tests, CI browser integration, generated code |
| [Releasing](docs/releasing.md) | Version tags, CI, release assets and checksums |

Start with [config.example.toml](config.example.toml) and
[behaviour_example.txt](behaviour_example.txt). Keep credentials exclusively in
the environment, never in TOML configuration or manifests.

## License

Looster's original code is [MIT licensed](LICENSE). Dependencies retain their
own licenses. See the [third-party notices](THIRD_PARTY_NOTICES.md) and
[licensing guide](docs/licensing.md), including HCL's MPL-2.0 source availability.
Run `looster licenses` to read the complete notices offline from any installed binary.
