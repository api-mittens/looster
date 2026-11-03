# Contributing

[Back to README](../readme.md) · [Architecture](architecture.md) · [Releasing](releasing.md)

## Development setup

Use the Go version in [go.mod](../go.mod), currently **1.27.2**. The module path is
`github.com/api-mittens/looster`. Private Go packages live under `internal/`, including
`internal/skills` and its bundled skill, and `internal/scripts` for installer tests.
The installer itself stays at `scripts/install.sh`. Keep the executable entry point
in `cmd/looster` and use `./internal/<package>` for focused package checks.
A normal build needs only Go:

```sh
go build -buildvcs=false -o bin/looster ./cmd/looster
```

For local end-to-end use, follow the [quick start](../readme.md#quick-start) with
Docker Compose and an independently authenticated Jcode installation. Use
`bin/looster` instead of `looster` if the binary is not on your `PATH`.

## Checks

The test suite is Go-only. No Node.js test runner or frontend package install is
required. Module behaviour runs in the test-only Goja interpreter against the
shipped JavaScript. Optional `-race` runs can be substantially slower for the
force-simulation stress cases, so CI runs the complete suite without that flag.

```sh
go test ./...
go test -race ./...
go vet ./...
go build -buildvcs=false -o bin/looster ./cmd/looster
```

Prefer focused table-driven unit tests and fake application ports. Unit tests use
a local fake Jcode executable and need no PostgreSQL, S3, authenticated model run,
or production service. Exercise user-visible changes manually as well as with
automated checks, using the Compose setup for infrastructure and real Jcode.

Optional PostgreSQL tests use `LOOSTER_TEST_DATABASE_URL`. Use a disposable test
database, never production. Namespace tests create unique temporary schemas and
remove only those schemas. Upgrade tests create and clean up disposable databases
and require `CREATEDB`; they verify that tasks, tags, histories, queues, sessions,
memories, and attachments survive migration. Infrastructure tests must not alter
existing application data. See [namespace tests](namespaces.md#integration-tests).

## CI and browser integration

[CI](../.github/workflows/ci.yml) builds the real CLI, starts PostgreSQL, selects
Chromium/Chrome, and runs `go test -count=1 -timeout=10m ./...` with both
browser opt-ins enabled. It then runs `go vet ./...`. Tagged release builds depend
on this test job succeeding.

Two Go-driven browser workflows cover different boundaries:

- `TestGraphBrowser` uses fixture API data with a real headless Chromium browser.
- `TestGraphProduction` uses a compiled Looster executable, disposable PostgreSQL
  namespaces, real CLI/API calls, and native browser pointer events. It does not
  substitute fixture HTTP responses or mocked SQL.

To run them locally, install Chromium or Chrome, use a disposable PostgreSQL
database (upgrade tests also require `CREATEDB`), and build the CLI first:

```sh
go build -buildvcs=false -o bin/looster ./cmd/looster
LOOSTER_BROWSER_TEST=1 go test -count=1 ./internal/web -run '^TestGraphBrowser$'
LOOSTER_PRODUCTION_TEST=1 \
  LOOSTER_TEST_BINARY="$PWD/bin/looster" \
  LOOSTER_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/looster_test?sslmode=disable' \
  go test -count=1 -timeout=5m ./internal/web -run '^TestGraphProduction$'
```

Set `CHROMIUM=/path/to/chromium` if automatic browser discovery is unsuitable.
The production test creates and cleans up unique test namespaces. Both workflows
run through Go, not Node.js. Without opt-in variables these browser tests skip;
a passing default unit run alone does not demonstrate browser acceptance.

## Generated code and migrations

Goose migrations are embedded in the binary. Commit sqlc query sources and their
generated Go bindings together. When changing queries, install the project's sqlc
version from [mise.toml](../mise.toml) and run:

```sh
sqlc generate
```

Use the configured Goose tooling when authoring migrations. Neither sqlc nor the
Goose CLI is required to install a release or run its embedded migrations.
Preserve existing data and document migration/rollback consequences. Do not rely
on a down migration where restoring a backup is the only lossless rollback.

## Design expectations

Keep idiomatic Go packages with inward dependencies and use application interfaces
for adapters. See [architecture](architecture.md). Keep credentials out of source,
manifests, logs, and test fixtures. Maintain configuration, help, examples, and docs
alongside user-visible changes. Generate the bundled agent skill with
`bin/looster skills generate` or `bin/looster skills generate ./project-skills`.
