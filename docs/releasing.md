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
