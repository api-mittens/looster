# Automatic DuckLake storage

Application commands, including `looster serve` and `looster web`, require
PostgreSQL and S3-compatible credentials. DuckLake is initialized automatically
before a repository is exposed. The selected application namespace is created
and migrated automatically, then the durable payload-writer fence is enabled.
Startup errors are fatal, not a switch back to PostgreSQL JSON payload storage.
Offline commands and local administrative commands such as `db migrate` do not
start DuckLake.

## Credentials-only setup

Provide the existing `PG*` settings or `LOOSTER_DATABASE_URL`, and
`LOOSTER_S3_BUCKET`, `LOOSTER_S3_ACCESS_KEY`, `LOOSTER_S3_SECRET_KEY`, and optionally
`LOOSTER_S3_ENDPOINT`, `LOOSTER_S3_REGION`, `LOOSTER_S3_SESSION_TOKEN`. The existing
AWS environment key fallback remains available when no explicit S3 credential
fields are set. Shared credential files, profiles and instance metadata are not
credential sources. Secrets remain environment-only, never TOML values.

The configured application database and bucket must already exist. The catalog
uses **the same PostgreSQL database**, with a separate metadata schema named
`looster_ducklake_<128-bit SHA256 namespace digest>`. The database role needs
permission to create that schema and own its catalog tables, not `CREATEDB`.
Application migrations retain their existing schema-owner requirements.

Each namespace has a dedicated S3 prefix:

```text
s3://<bucket>/ducklake/namespaces/<namespace>/
```

Hyphens and underscores in namespace names remain distinct. Different Looster
installations sharing a bucket should use different `ducklake.data_path` roots.
The configured PostgreSQL TLS settings are preserved for DuckLake's native
connector. Verification modes require an explicit root certificate path, and
unsupported connector settings fail closed rather than weaken TLS. Password-file
lookup is disabled. Keep credentials in the resolved connection environment,
not `.pgpass` or service profiles. The embedded connector does not mutate
process-wide `PG*` variables.

## Embedded engine and signed extensions

All lake operations use the built-in official `github.com/duckdb/duckdb-go/v2`
client through `database/sql`, with a statically linked DuckDB core. No separate
DuckDB executable, package, catalog server or engine download is required.
Release binaries support Linux and macOS on amd64 and arm64, not Windows.
Source builds and native engine tests require CGO and a C/C++ toolchain.

The pinned Go client is **v2.10502.0**, embedding **DuckDB v1.5.2**. This is an
explicit compatibility selection for DuckLake 1.0, not a claim that it is the
latest engine. Engine and signed extension build IDs are checked before attach:

| Component | Pin |
| --- | --- |
| DuckDB | v1.5.2 |
| DuckLake format | 1.0 |
| ducklake | 415a9ebd |
| postgres_scanner | c89234f |
| httpfs | 13e18b3 |

Signed native extensions still require outbound HTTPS to the official
`extensions.duckdb.org` repository on first use and a writable user cache.
Subsequent initialization can use compatible cached extensions. An empty cache
cannot initialize offline. Unsigned/community extensions, HTTP mirrors,
TLS-verification bypasses and insecure retries are not enabled. Protect cached
extensions from modification by other users. Incompatible cached artifacts fail
closed. Native diagnostics are suppressed because SQL errors can echo credentials.
Do not log resolved connection strings, credential-bearing config or SQL.

Each core database has a **256 MiB DuckDB buffer-manager memory limit** at
connection creation. This is not a total-process RSS limit: native allocations
and some operators can allocate outside the buffer manager.

## Optional non-secret configuration

```toml
[ducklake]
# database = "existing_catalog" # Default: application database, not a new DB
# data_path = "s3://my-bucket/custom-ducklake-root/"
# spool_dir = "/durable/local/looster-spool"
```

Equivalent environment keys are `LOOSTER_DUCKLAKE_DATABASE`,
`LOOSTER_DUCKLAKE_DATA_PATH`, and `LOOSTER_DUCKLAKE_SPOOL_DIR`. A data path is a
**root**: Looster appends `namespaces/<namespace>/`, even when overridden. A
separate catalog override must refer to an existing database at runtime.

Pending payloads are durably spooled locally while bounded DuckLake batch
writes execute. A successful Put acknowledges **remote commit**, not merely a
local spool write, so another host can immediately resolve a PostgreSQL payload
reference. Failed writes preserve pending spool records for retry. The default
spool root is `$XDG_STATE_HOME/looster/ducklake-spool`, or
`~/.local/state/looster/ducklake-spool`. Keep it on durable local storage. Spools
are isolated by catalog, S3 path and namespace, independent of credentials so
password rotation does not strand pending writes. Shutdown attempts a final
flush and preserves pending files on failure. S3 must permit Parquet writes and
reads, and access to its namespace prefix.

## Existing configuration command

`looster configure storage` remains a human-only, local PostgreSQL provisioning
and migration command. It does not need S3 or download extensions unless asked
to prewarm DuckLake. It retains existing databases. Provisioning may require
`CONNECT` to the `postgres` maintenance database and `CREATEDB` **only when a
database is actually missing**. For managed installations, have the administrator
create the application database first and use `looster db migrate`.
Normal application startup never provisions PostgreSQL databases.

```sh
looster configure storage
looster configure storage --database my_existing_looster
looster configure storage --ducklake
looster configure storage --ducklake --ducklake-data-path s3://my-bucket/custom-root/
```

The default is the resolved, explicitly configured database. A fresh installation
without a database selection uses `looster`. The `--database` option changes only
the database component, retaining host, TLS and authentication settings. It does
not persist runtime configuration. Set `PGDATABASE` or the database component of
`LOOSTER_DATABASE_URL` consistently afterward. Explicit connection-string fields
take precedence over environment fallbacks.

`--ducklake` is a prewarm option, not runtime opt-in. The
`--ducklake-database` and `--ducklake-data-path` flags remain accepted with
`--ducklake`. An explicitly requested separate catalog database is still
provisioned by this administrative command. Overrides are not persisted.
Configure the corresponding runtime environment or TOML keys consistently
afterward. Old standalone probe catalogs and payload-free TSK-57 catalogs are
not automatically migrated or deleted. `looster db migrate` only migrates the
selected existing application database/namespace, never provisions databases
or initializes DuckLake.

## Safe verification

For coordinated upgrades, guarded historical backfill and the explicit
schema-removal safety gate, see
[Lake payload cutover and historical migration](storage-migration.md).

Unit tests do not require or mutate PostgreSQL/S3 infrastructure:

```sh
go test ./internal/ducklake ./internal/cli
```

For a reproducible native-engine run using fresh PostgreSQL and loopback S3
fixtures, see [the disposable harness](../scripts/test-ducklake/README.md).
The harness never loads normal application connection settings.

A real Parquet write/reopen probe remains explicitly opt-in:
`TestDisposableIntegration` requires `LOOSTER_DUCKLAKE_DISPOSABLE_PROBE=yes` and
all its `LOOSTER_TEST_*` credentials. Use only an exclusively owned disposable
catalog and S3 prefix. The probe expires snapshots and deletes files. Never run
it against live task infrastructure, shared storage or production. This probe's
destructive cleanup does not authorize cleanup of application payload storage.
