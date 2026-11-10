# Automatic DuckLake storage

Application commands, including `looster serve` and `looster web`, require
PostgreSQL and S3-compatible credentials. DuckLake is initialized automatically
before a repository is exposed. Startup errors are fatal, not a switch back to
PostgreSQL JSON payload storage. Offline commands and local administrative
commands such as `db migrate` do not start DuckLake.

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
unsupported connector settings fail closed rather than weaken TLS.

## Automatic engine and extension acquisition

No DuckDB package, separate catalog database, extra service or manual extension
installation is needed. On first startup Looster downloads the pinned DuckDB
**v1.5.2** CLI release from GitHub over verified HTTPS and verifies its compiled-in
SHA256 archive digest before extraction/execution. Supported automatic platforms
are macOS and Linux on arm64 and amd64. Cached archives are rehashed before use,
and the executable is reconstructed atomically from verified bytes. A corrupt
cache fails closed. Remove the affected local cache archive to retry downloading.

Official extensions are downloaded over verified HTTPS by Go, then installed
locally and loaded by DuckDB with its normal signature checks. This avoids the
HTTP bootstrap that remote `INSTALL` would otherwise need to obtain `httpfs`.
Unsigned extensions, HTTP extension mirrors, TLS-verification bypasses and
insecure retry paths are not enabled. The engine and extension build IDs are
checked before attaching the catalog:

| Component | Pin |
| --- | --- |
| DuckDB | v1.5.2 |
| DuckLake format | 1.0 |
| ducklake | 415a9ebd |
| postgres_scanner | c89234f |
| httpfs | 13e18b3 |

Artifacts are cached under the OS user cache directory in `looster/duckdb/`.
Network access to GitHub release assets and `extensions.duckdb.org` is required
for a cold start. Subsequent starts use cached artifacts and installed signed
extensions. Errors suppress engine diagnostics because SQL diagnostics can
contain PostgreSQL or S3 credentials.

## Optional non-secret configuration

```toml
[ducklake]
# binary = "/opt/duckdb/duckdb" # Explicit trusted pinned executable override
# database = "existing_catalog" # Default: application database, not a new DB
# data_path = "s3://my-bucket/custom-ducklake-root/"
# spool_dir = "/durable/local/looster-spool"
```

Equivalent environment keys are `LOOSTER_DUCKLAKE_BINARY`,
`LOOSTER_DUCKLAKE_DATABASE`, `LOOSTER_DUCKLAKE_DATA_PATH`, and
`LOOSTER_DUCKLAKE_SPOOL_DIR`. A data path is a **root**: Looster appends
`namespaces/<namespace>/`, even when overridden. A separate catalog override must
refer to an existing database at runtime. An explicit binary override is
operator-trusted, still version checked, and does not disable signed extension
verification.

Acknowledged payloads are durably spooled locally until bounded DuckLake batch
writes succeed. The default spool root is
`$XDG_STATE_HOME/looster/ducklake-spool`, or
`~/.local/state/looster/ducklake-spool`. Keep it on durable local storage. Spools
are isolated by catalog, S3 path and namespace, independent of credentials so
password rotation does not strand pending writes. Shutdown attempts a final
flush and preserves pending files on failure. S3 must permit Parquet writes and
reads, and access to its namespace prefix.

## Existing configuration command

`looster configure storage` remains a human-only, local PostgreSQL provisioning
and migration command. It does not need S3 or download the engine unless asked
to prewarm DuckLake. It retains existing databases. Provisioning may require
`CONNECT` to the `postgres` maintenance database and `CREATEDB` **only when an
application database is actually missing**. For managed installations, have the
administrator create the application database first and use `looster db migrate`.
Normal application startup never provisions PostgreSQL databases.

```sh
looster configure storage
looster configure storage --ducklake
looster configure storage --ducklake --ducklake-data-path s3://my-bucket/custom-root/
```

`--ducklake` is now a prewarm option, not runtime opt-in. The legacy
`--ducklake-database`, `--ducklake-data-path`, and `--duckdb-binary` flags remain
accepted with `--ducklake`. The default catalog is now the application database,
and the default binary is automatically acquired. An explicitly requested
separate catalog database is still provisioned by this administrative command.
Overrides are not persisted. Configure the corresponding runtime environment or
TOML keys consistently afterward. Old standalone probe catalogs and payload-free
TSK-57 catalogs are not automatically migrated or deleted.

## Safe verification

Unit tests do not require or mutate PostgreSQL/S3 infrastructure:

```sh
go test ./internal/ducklake ./internal/cli
# Downloads only public artifacts into temporary local directories:
LOOSTER_TEST_ENGINE_BOOTSTRAP=yes go test ./internal/ducklake -run TestEngineBootstrapArtifacts -count=1
```

A real Parquet write/reopen probe remains explicitly opt-in:
`TestDisposableIntegration` requires `LOOSTER_DUCKLAKE_DISPOSABLE_PROBE=yes` and
all its `LOOSTER_TEST_*` credentials. Use only an exclusively owned disposable
catalog and S3 prefix. The probe expires snapshots and deletes files. Never run
it against live task infrastructure, shared storage or production.
