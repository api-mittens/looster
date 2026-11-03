# Installation

[Back to README](../readme.md)

## Release binary (no Go required)

Linux and macOS users can download and inspect the installer before executing it:

```sh
curl -fsSL https://raw.githubusercontent.com/api-mittens/looster/master/scripts/install.sh -o install-looster.sh
# Inspect the downloaded file first.
sh install-looster.sh
export PATH="$HOME/.local/bin:$PATH"
looster --help
```

| Environment variable | Purpose | Default |
| --- | --- | --- |
| `LOOSTER_VERSION` | Select a release using its required `v` prefix | Latest release |
| `LOOSTER_INSTALL_DIR` | Binary destination | `~/.local/bin` |
| `LOOSTER_REPO` | GitHub owner/repository to download from | `api-mittens/looster` |

For example, pin a version and change the destination (replace the illustrative
version with a published release):

```sh
LOOSTER_VERSION=v0.1.0 LOOSTER_INSTALL_DIR="$HOME/bin" sh install-looster.sh
```

Add your chosen installation directory to your shell's persistent `PATH` too.
The installer is for Unix systems. On Windows, use a release ZIP instead.

## Manual downloads

Open the repository's GitHub **Releases** page at
`https://github.com/api-mittens/looster/releases` and choose an archive matching
your operating system and CPU:

| Operating system | Architectures | Archive |
| --- | --- | --- |
| Linux | `amd64`, `arm64` | `.tar.gz` |
| macOS (`darwin`) | `amd64`, `arm64` | `.tar.gz` |
| Windows | `amd64`, `arm64` | `.zip` |

Download the release checksums alongside the archive and verify its SHA-256
before extracting. On Linux use `sha256sum`, on macOS `shasum -a 256`, or on
PowerShell `Get-FileHash -Algorithm SHA256`. Compare the result with the checksum
for that exact archive. Put `looster` (or `looster.exe`) in a directory on `PATH`.
Do not execute a download whose checksum differs.

The release executable includes the dashboard assets and migrations. It does not
need Go, Node.js, npm, a bundler, or a runtime CDN. Running workers still requires
Jcode and PostgreSQL, plus configured S3-compatible storage for files and archives.
Authenticate Jcode independently before starting workers.

The executable also retains Looster's MIT license and complete dependency notices.
Run `looster licenses` offline to read them, including the exact source location
for the MPL-2.0 HCL dependency. Release archives include the license documents and
HCL source ZIP separately. See [licensing](licensing.md) before redistributing.

## Build from source

The module is `github.com/api-mittens/looster`. Use **Go 1.27.2 or newer**, as declared
in [go.mod](../go.mod). From the source checkout:

```sh
go build -buildvcs=false -o bin/looster ./cmd/looster
./bin/looster --help
```

Use `./bin/looster` in place of `looster` in the [quick start](../readme.md#quick-start),
or put the built executable on `PATH`. sqlc and Goose command-line tools are not
needed for a normal build. See [contributing](contributing.md) for development.

## Updating

Back up PostgreSQL and stop old workers before installing an update. Run
`looster db migrate` for every existing namespace, review the active manifest,
then restart workers. Do not run old and new workers together during lifecycle
migrations. See [upgrade notes](usage.md#namespaces-and-upgrades) for legacy
status/rule conversions and backup-based rollback requirements.
