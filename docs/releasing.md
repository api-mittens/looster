# Releasing

[Back to README](../readme.md) · [Contributing](contributing.md)

## Prepare

1. Run the [Go checks](contributing.md#checks), review generated code and migration
   changes, and update documentation for user-visible changes. Regenerate and review
   third-party notices with `go run ./scripts/licenses`, then run
   `go run ./scripts/licenses -check`. See [licensing](licensing.md).
2. Confirm source imports use `github.com/api-mittens/looster` and the CI toolchain
   follows [go.mod](../go.mod).
3. Check the installer, example config, behaviour manifest, and fresh-install and
   upgrade workflows. Never test upgrades against production data.
4. Choose a new semantic version with a `v` tag, for example `v0.1.0`. The example
   is illustrative, not a statement that this version has been published.

## Publish a version tag

From an established Git checkout with the release commit already pushed:

```sh
git tag -a v0.1.0 -m 'Release v0.1.0'
git push origin v0.1.0
```

Pushing a `v` version tag triggers CI. CI runs the Go tests first, then builds
and publishes release assets and SHA-256 checksums.

CI includes Go-driven Chromium browser acceptance tests and PostgreSQL integration
tests before releasing. See [CI and browser integration](contributing.md#ci-and-browser-integration).
A failed test run must not produce a release. No Node.js tests or frontend build
are part of this workflow.

The release matrix is:

| OS | Architectures | Archive format |
| --- | --- | --- |
| `linux` | `amd64`, `arm64` | `.tar.gz` |
| `darwin` | `amd64`, `arm64` | `.tar.gz` |
| `windows` | `amd64`, `arm64` | `.zip` |

Archives contain the platform's executable (`looster` or `looster.exe`). The
release also includes checksums for the archives so downloads can be verified.
The dashboard and migrations are embedded, so users need no Go toolchain to run
the release binary. Each archive also includes Looster's `LICENSE`, the complete
`THIRD_PARTY_NOTICES.md`, and `third_party_sources/hashicorp-hcl-<version>.zip`
for the MPL-2.0 HCL dependency. The binary embeds all notices, accessible offline
with `looster licenses`, including after the installer extracts only the executable.

## Embedded native engine build requirements

Every published target uses cgo. `CGO_ENABLED=0` and cross-compiling the old
pure-Go matrix are no longer supported. The core engine is statically linked,
not loaded from a user's DuckDB installation. This does not make every OS runtime
or framework static. No `duckdb_use_lib` build is publishable.

| Target | Native runner | DuckDB core strategy |
| --- | --- | --- |
| Linux amd64 | Ubuntu 24.04 amd64 | Official version-pinned prebuilt static archive |
| Linux arm64 | Ubuntu 24.04 ARM | Official version-pinned prebuilt static archive |
| macOS amd64 | macOS 15 Intel | Official version-pinned prebuilt static archive |
| macOS arm64 | macOS 15 ARM | Official version-pinned prebuilt static archive |
| Windows amd64 | Windows 2025, MSYS2 UCRT64 | Official version-pinned prebuilt static archive |
| Windows arm64 | Windows 11 ARM, MSYS2 CLANGARM64 | Source-pinned custom static archive with `duckdb_use_static_lib` |

Windows ARM64 has no upstream prebuilt Go archive. Its explicit recipe is
`scripts/build-duckdb-windows-arm64.sh`, using the same exact DuckDB source as
the pinned driver and including the generated extension loader. It must compile,
link, execute the native tests, and acquire matching **signed** extensions on
that native runner before the target is release-ready. A source recipe alone is
not evidence of support. Unavailable signed extensions or any failing target
block the entire six-artifact release, rather than silently omitting Windows ARM64.

Every native build runs `scripts/release-archive.sh`, which inspects runtime
imports with `readelf`, `otool` or `objdump`. Unbundled DuckDB/C++/pthread
libraries are rejected. System C libraries/frameworks are allowed. Linux builds
use the GNU/glibc environment, not musl or Alpine, and statically link the C++
runtime. Windows runtime imports must be system DLLs only. The resulting binary
runs `--help`, and engine lifecycle and cold-cache signed extension tests run
without a host DuckDB CLI. CI also runs an isolated PostgreSQL/S3 write/reopen
harness, never production catalog cleanup. All six native jobs run on PRs as
well as tags, and release publication requires all six archives.

Archives retain native engine notices, runtime-specific compiler library notices,
and the existing HCL source ZIP. See
[the native licensing inventory](../scripts/licenses/README.md) for exact
source pins, the TPC/ICU notice collections, and regeneration checks. Runtime
extension downloads are separately acquired signed executable code, not bundled
inside the release binary. First-use networking and offline cache limitations
are documented in [storage](storage.md).

Before publishing, record actual native CI results for this commit. A local
macOS ARM64 build validates only that platform, not the other five. Never bypass
failed signed-extension, linking, dependency, or disposable write/reopen gates.

## Verify the published release

- Confirm CI tests passed before the release builds, all six archives exist, and
  every archive has a matching checksum entry.
- Download and verify an archive, extract it, and run `looster --help` and
  `looster licenses`. Check the MIT/dependency notices and the HCL source archive.
- Check the installer with the published `v`-prefixed tag in `LOOSTER_VERSION`.
  Versions without the required `v` prefix are rejected. Verify a custom `LOOSTER_INSTALL_DIR` and the default
  `LOOSTER_REPO=api-mittens/looster` source.
- Verify a clean Compose setup with [config.example.toml](../config.example.toml)
  and [behaviour_example.txt](../behaviour_example.txt), then review the migration notes.
- Do not silently replace a published version's artifacts. Correct a broken
  release with a new version and explain upgrade implications.

These instructions describe publishing from an existing repository. They do not
require initializing Git in an unversioned source directory.
