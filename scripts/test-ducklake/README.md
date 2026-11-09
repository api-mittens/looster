# Disposable DuckLake integration harness

Run from a checkout with the embedded DuckDB CGo toolchain available:

```sh
JCODE_SCRATCH_DIR="$HOME/.cache/looster-integration" ./scripts/test-ducklake.sh
```

The runner creates a fresh PostgreSQL cluster under the scratch directory,
starts PostgreSQL on an ephemeral loopback TCP port with local trust auth,
creates an empty catalog database, and starts an in-memory loopback S3 fixture.
It runs these uncached, explicitly enabled test groups in sequence:

- `go test -count=1 -v -timeout=5m ./internal/postgres -run '^TestProvisionIntegration'`
- `go test -count=1 -v -timeout=5m ./internal/ducklake -run '^TestDisposableIntegration$'`

The latter writes real Parquet through the embedded engine, closes that engine,
reopens the PostgreSQL catalog in a fresh engine, reads the persisted row, and
cleans up. Nothing needs Docker, MinIO, application configuration or an existing
PostgreSQL database. Ambient `PG*`, `AWS*`, and `LOOSTER*` connection settings are
not forwarded to either database tools or tests. Test endpoints and credentials
are generated locally and supplied explicitly. The only runner override is
`LOOSTER_TEST_PG_BIN`, which defaults to
`/opt/homebrew/opt/postgresql@17/bin` and must contain `initdb`, `pg_ctl`, and
`createdb`. PostgreSQL cannot run as root.

The runner shuts down the fixture and cluster and removes its scratch directory
on success, failure, timeout, SIGINT or SIGTERM. SIGKILL cannot be trapped. The
usual Go module/build caches and DuckDB signed-extension cache may remain.
Official HTTPS extension installation requires network access on a cold cache.

The fixture implements path-style PUT, GET, HEAD, DELETE, byte ranges,
ListObjectsV2 prefix filtering, and multipart initiate/upload/complete/abort.
It is intentionally **not** a production S3 server: no authentication/signature
verification, TLS, persistence, full listing pagination, or full AWS error
compatibility. Objects and multipart parts are intended only for small probes
(up to 128 MiB per upload request). Local trust PostgreSQL and unauthenticated
S3 are restricted to loopback, not suitable for shared untrusted machines.
A local fixture pass does not establish real-cloud IAM/TLS compatibility.

Fixture protocol checks are available without PostgreSQL or embedded DuckDB:

```sh
go test -race ./internal/ducklakefixture
```
