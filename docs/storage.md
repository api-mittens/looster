# Optional storage configuration (TSK-57)

`looster configure storage` is a **human-only**, local bootstrap command. It
creates missing databases on the configured PostgreSQL server and runs embedded
application migrations. Existing databases are retained, never renamed, dropped
or replaced. Normal Looster commands still use PostgreSQL and do not need DuckDB,
DuckLake extensions, S3 credentials or a catalog connection.

## PostgreSQL only

Load database credentials through the environment as described in `.env.example`.
Use your existing `LOOSTER_DATABASE_URL` or native `PG*` settings, then run:

```sh
looster configure storage
# Explicit application database selection, including existing custom names:
looster configure storage --database my_existing_looster
```

The default is the resolved, explicitly configured database. A fresh installation
without a database selection uses `looster`. The `--database` option changes only
the database component, retaining host, TLS and authentication settings. **It does
not persist runtime configuration.** Set `PGDATABASE` or the database component of
`LOOSTER_DATABASE_URL` consistently afterward so subsequent commands keep using
the same data. Explicit connection-string fields take precedence over environment
fallbacks, including when `PGDATABASE` is also set.

Provisioning connects to the `postgres` maintenance database on the same server.
The login needs `CONNECT` there and `CREATEDB` only if a database is missing.
Otherwise an administrator can create the databases first, owned by the migration
login. Schema ownership/migration permissions are still required. Creation checks
are idempotent and tolerate concurrent creators. The selected application
namespace is created and migrated with its existing namespace lock and Goose
history. Nondefault PostgreSQL namespaces remain isolated.

`looster db migrate` remains unchanged: it only migrates the selected existing
application database/namespace and never provisions databases or DuckLake.

## Explicit DuckLake opt-in

DuckLake stores metadata in a **separate PostgreSQL database** (default
`ducklake`) and data files in a dedicated S3 prefix. This release supports only
Looster's `default` namespace for DuckLake. Nondefault namespace requests fail
before provisioning, rather than sharing a catalog or prefix accidentally.
Nothing in this task moves session data, event logs or existing tables.

Install the pinned DuckDB CLI and its compatible official `ducklake`, `postgres`
and `httpfs` extensions before opting in. Looster loads installed extensions
lazily and disables automatic extension installation/loading. There is no native
DuckDB library in the Looster binary and no new database server to run. See the
engine pins and installation instructions below.

Keep PostgreSQL passwords, S3 access/secret keys and optional session tokens in
the environment. Existing `LOOSTER_S3_*` settings are used, with the same explicit
AWS environment fallback policy as attachment storage. No AWS profile, credential
process or instance metadata provider is used. Custom HTTP/HTTPS endpoints,
region and path-style addressing are supported. Use a dedicated bucket/prefix,
not the attachment prefix or another installation's lake data path.

```sh
# Configure the normal application PG environment and LOOSTER_S3_* first.
looster configure storage --ducklake \
  --ducklake-database ducklake \
  --ducklake-data-path s3://my-bucket/looster-ducklake/default/
# Optional executable override:
#   --duckdb-binary /opt/duckdb/duckdb
```

The catalog must differ from the resolved application database. The S3 bucket
must already exist. Metadata attachment initializes the catalog, but does **not**
by itself prove S3 permissions or endpoint reachability. Use the disposable
write/reopen verification below before considering a deployment ready for data.
Database creation and migrations cannot be rolled back together with catalog
attachment. If attachment fails, the command explicitly reports completed
application migrations and retains the databases. Correct prerequisites and retry
with the same names and path. Do not delete or switch an existing catalog to
recover from an error.

Credentials travel to the child DuckDB process only over stdin, in temporary
in-memory secrets. They are not placed in process arguments, generated files,
TOML, command output or persistent DuckDB secrets. Child diagnostics are
suppressed because PostgreSQL/DuckDB errors can echo secrets. Errors identify
which stage failed, but troubleshooting must not involve logging SQL or resolved
connection strings. TLS and PostgreSQL options that cannot be faithfully passed
to the libpq connector are rejected rather than silently weakened.

### Pinned engine and extensions

The supported engine is **DuckDB v1.5.2**, with official signed core extension
build IDs `ducklake=415a9ebd`, `postgres_scanner=c89234f` and `httpfs=13e18b3`.
The DuckLake metadata format is 1.0. Looster checks the engine and all three
extension build IDs before attaching, rather than trusting a moving release label.
Download the v1.5.2 CLI for your platform from the official DuckDB release and
verify its published checksum. Install extensions manually with that exact CLI:

```sh
printf '%s\n' 'INSTALL ducklake; INSTALL postgres; INSTALL httpfs;' | \
  /opt/duckdb/duckdb -init /dev/null -batch -bail :memory:
printf '%s\n' "SELECT extension_name, extension_version FROM duckdb_extensions() WHERE installed;" | \
  /opt/duckdb/duckdb -init /dev/null -batch -bail :memory:
```

Keep the signed artifacts under the invoking user's DuckDB extension directory.
No extensions are downloaded by `configure storage`. If installed versions do
not match the pins, initialization fails safely. Updating engine/extensions is an
explicit future compatibility change, not automatic production behavior.

## Reusable engine boundary

`internal/ducklake.Open` accepts a resolved catalog connection and storage config.
`Lake.Exec` attaches the alias `lake` in a fresh in-memory process for each call,
so a transaction must fit in one call. It is a private boundary for future trusted
writers, not a public arbitrary-SQL CLI. Runtime session/event storage remains
unchanged. Application code must not log the credential-bearing config or SQL.

## Disposable write/reopen verification

`ducklake.ProbeDisposable` explicitly requires confirmation of an **exclusively
owned disposable catalog and S3 prefix**. It writes a Parquet-backed table with
inlining disabled, exits the writer, reopens the catalog and checks the row, then
drops the table and expires/cleans files. This cleanup is catalog-wide and must
**never** run on production or a shared catalog. A failed probe can leave test
artifacts. The gated integration test uses only explicitly supplied test settings,
not the ordinary application database URL. Test fixtures must be provisioned
separately, with test-only credentials.

With an isolated PostgreSQL cluster, test bucket and the pinned CLI/extensions
already prepared, set these **test-only** variables in your trusted shell:

```sh
# Database provisioning tests create and drop unique databases and roles.
LOOSTER_TEST_PROVISION_DATABASE_URL='postgres://postgres:test@127.0.0.1:55457/postgres?sslmode=disable' \
  go test ./internal/postgres -run TestProvisionIntegration -count=1 -v

# DuckLake test catalog must already exist and be exclusively disposable.
export LOOSTER_TEST_DUCKDB=/opt/duckdb/duckdb
export LOOSTER_TEST_DUCKLAKE_LIBPQ='host=127.0.0.1 port=55457 dbname=ducklake_test user=postgres password=test sslmode=disable'
export LOOSTER_TEST_DUCKLAKE_DATA_PATH=s3://looster-test/disposable/default/
export LOOSTER_TEST_S3_ENDPOINT=http://127.0.0.1:55458
export LOOSTER_TEST_S3_REGION=us-east-1
export LOOSTER_TEST_S3_ACCESS_KEY=test
export LOOSTER_TEST_S3_SECRET_KEY=test
LOOSTER_DUCKLAKE_DISPOSABLE_PROBE=yes \
  go test ./internal/ducklake -run TestDisposableIntegration -count=1 -v
```

The probe requires explicit test credentials and never falls back to ordinary
application settings. It also checks that metadata-only attach can succeed with
an unreachable S3 endpoint while a real Parquet write fails safely. Unit tests
run without DuckDB, PostgreSQL or S3. An S3 emulator verifies the HTTP persistence
workflow, not cloud IAM policies, TLS certificates or production availability.

## Official references

- https://ducklake.select/docs/1.0/duckdb/usage/connecting
- https://ducklake.select/docs/1.0/duckdb/installation
- https://ducklake.select/release_calendar
- https://duckdb.org/docs/stable/configuration/secrets_manager
- https://duckdb.org/docs/stable/core_extensions/postgres
- https://duckdb.org/docs/stable/core_extensions/httpfs/s3api
