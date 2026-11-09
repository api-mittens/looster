# Windows AMD64 MinGW PostgreSQL extension release blocker

Verified November 9, 2026 against DuckDB v1.5.2. This is an unresolved
compatibility blocker, not a supported-platform exception. The six-platform
build matrix and mandatory extension bootstrap gate must remain enabled.
A successful executable link or `--help` invocation alone does not establish
that the PostgreSQL-backed DuckLake workflow is usable on this target.

## Exact upstream source evidence

* DuckDB source commit `8a5851971fae891f292c2714d86046ee018e9737`,
  `.github/config/extensions/postgres_scanner.cmake`, lines 3-9, explicitly
  excludes `MINGW` from PostgreSQL extension configuration. It pins
  `duckdb/duckdb-postgres` commit
  `c89234f0b1985f4ee0f52f16e742a1ab2d4ae4f0` and the
  `database-connector` submodule.
* At that exact PostgreSQL extension commit,
  `.github/workflows/MainDistributionPipeline.yml`, lines 26 and 37,
  excludes `windows_amd64_mingw` from both build and deployment.
* Its `CMakeLists.txt` pins PostgreSQL `REL_15_13`, compiles libpq sources into
  the extension, requires static OpenSSL, and on `WIN32` configures PostgreSQL
  with `perl mkvcbuild.pl` in `postgres/src/tools/msvc` (lines 155-168).
  Adding a static extension target does not supply a tested MinGW PostgreSQL
  configuration or matching static OpenSSL libraries.
* The pinned `database-connector` submodule commit is
  `746b56c4063f3682f4eb4facdc49408ed1885555`.
* Upstream issue `duckdb/duckdb-postgres#306`, maintainer reply on April 8,
  2025, identifies an out-of-box MinGW compilation problem. The explicit
  exclusion remains in the exact release source above and in upstream main
  commit `8b9d0bbe8de4c57fb90d9e2530f9ce39f8c4baf3` examined on the verification
  date. Main has since changed to external libpq 18.4 and is not an exact-version
  drop-in for the pinned release.

Primary source locations:

```text
https://github.com/duckdb/duckdb/blob/8a5851971fae891f292c2714d86046ee018e9737/.github/config/extensions/postgres_scanner.cmake
https://github.com/duckdb/duckdb-postgres/blob/c89234f0b1985f4ee0f52f16e742a1ab2d4ae4f0/.github/workflows/MainDistributionPipeline.yml
https://github.com/duckdb/duckdb-postgres/blob/c89234f0b1985f4ee0f52f16e742a1ab2d4ae4f0/CMakeLists.txt
https://github.com/duckdb/duckdb-postgres/issues/306
```

## Official repository probes

HTTPS HEAD requests to
`https://extensions.duckdb.org/v1.5.2/<platform>/<extension>.duckdb_extension.gz`
returned:

| Platform | Extension | HTTP status |
| --- | --- | --- |
| windows_amd64_mingw | httpfs | 200 |
| windows_amd64_mingw | ducklake | 200 |
| windows_amd64_mingw | postgres_scanner | 404 |
| windows_amd64_mingw | postgres | 404 |
| windows_amd64 | postgres_scanner | 200 |
| windows_arm64 | postgres_scanner | 200 |
| windows_arm64_mingw | httpfs | 404 |
| windows_arm64_mingw | ducklake | 404 |
| windows_arm64_mingw | postgres_scanner | 404 |

These responses establish artifact availability only, not signature validation.
The `postgres` alias is not an alternative downloadable artifact. The MSVC
`windows_amd64` binary cannot be substituted for the actual engine's
`windows_amd64_mingw` C++ ABI. A platform override, unsigned extension setting,
external DLL dependency, or skipped bootstrap test is not an acceptable fix.

## Windows ARM64 has a separate C++ ABI blocker

The custom CLANGARM64 compiler uses the MinGW GNU C++ ABI and libc++. DuckDB's
exact source `src/include/duckdb/common/platform.hpp` adds the `_mingw` suffix
when `__MINGW32__` is defined. Thus its real identity is
`windows_arm64_mingw`, not the MSVC `windows_arm64` identity. The build script
must not set `DUCKDB_EXPLICIT_PLATFORM=windows_arm64` to conceal that difference.
The official `windows_arm64` C++ extension binaries are not interchangeable
merely because both targets use the ARM64 instruction set. All three required
artifacts for the real MinGW ARM64 identity returned 404 above.

This target consequently needs either a genuinely MSVC-ABI-compatible core
and C bridge with verified static runtime linkage, or all required extensions
built into the exact custom engine from pinned sources. Building only the
seven existing bundled extensions does not provide httpfs, ducklake or
postgres_scanner. Neither alternative has been validated in this recipe.

## Required resolution before release

Either obtain an upstream signed extension for the exact DuckDB version and
MinGW platform, or implement and validate a fully source-pinned custom static
engine containing `postgres_scanner` and its real generated extension loader.
The latter requires a working MinGW PostgreSQL configuration, exact PostgreSQL
and database-connector sources, static OpenSSL with all transitive dependencies,
source checksums and license inventories for every newly embedded component.
It must pass native Windows AMD64 build and dependency inspection plus a real
PostgreSQL-backed DuckLake bootstrap/read/write integration test. Source-building
the extension into the executable does not require allowing unsigned dynamic
extensions. No such custom build has been validated by the current recipe.

Until one of these paths is demonstrated, the release remains blocked. Keep all
six target jobs and the release dependency on their success, rather than
publishing five targets or reporting the Windows AMD64 smoke test as full
workflow acceptance.
