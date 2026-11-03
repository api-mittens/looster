# Architecture

[Back to README](../readme.md)

All application packages are private under `internal/`. The executable entry point
remains `cmd/looster`. Embedded migrations, dashboard assets, and the bundled
agent skill live alongside their owning packages. `scripts/install.sh` remains
at the repository root for release packaging and installation.

- Pure domain packages: `internal/task`, `internal/agent`, `internal/tag`, `internal/memory`, `internal/event`, `internal/behaviour`, and `internal/namespace`.
- `internal/app` defines repository, manifest, queue, harness, and object-storage ports and orchestrates work.
- `internal/postgres`, `internal/jcode`, and `internal/objectstore` implement those ports.
- `internal/cli` is the composition root. `internal/web` exposes the dashboard using application ports.
- Goose migrations and sqlc query bindings live in `internal/postgres`.

Dependencies point inward. Domain packages do not import application or
infrastructure packages. `internal/app/architecture_test.go` enforces this boundary.
Use application interfaces for adapters and keep domain logic independent of I/O.

Human and agent permissions are enforced in the application. Agents may work
with tasks and shared tag memories, but cannot administer manifests, namespaces,
other agents' memories, or other agents' session bindings. These are application
roles, not authenticated user accounts or a sandbox for hostile processes.
See [roles and database setup](usage.md#application-roles-and-database-setup).

The CLI talks directly to PostgreSQL through one shared login. Workers consume
durable jobs without needing an HTTP listener. Multiple servers can share agents;
connection-scoped advisory locks serialize each agent/task pair. Status changes
and dispatch are committed together using database triggers. Use direct database
connections or session pooling, not transaction pooling.

Task state and session state are separate. Tasks use `planning`, `todo`,
`validated`, and `closed`; sessions use `running`, `idle`, and `failed`.
Each agent/task pair has a resumable Jcode session. Completed history is staged
in PostgreSQL and archived to S3-compatible storage. See
[behaviour and sessions](usage.md#behaviour-and-sessions) for scheduling,
notifications, recovery, and at-least-once execution semantics.
