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

## Install with mise (alternative)

If you already use [mise](https://mise.jdx.dev/), its
[Go backend](https://mise.jdx.dev/dev-tools/backends/go.html) can build and manage
Looster instead of the release installer. Install mise first. Unlike release
binaries, this method requires the Go toolchain declared in [go.mod](../go.mod)
(currently **Go 1.27.2 or newer**) and access to the Go module and its dependencies.
The tool identifier includes `/cmd/looster`, the executable package, not just the
module root.

For a user-wide installation:

```sh
mise use --global go@1.27.2
mise use --global go:github.com/api-mittens/looster/cmd/looster@latest
mise exec -- looster --help
```

For a repository-local installation, run these commands from the repository where
you want to use Looster (it need not be Looster's own source checkout):

```sh
mise use go@1.27.2
mise use go:github.com/api-mittens/looster/cmd/looster@latest
mise exec -- looster --help
```

Without `--global`, `mise use` writes the tools to the project's mise config and
installs them. Alternatively, add these entries to your repository's `mise.toml`
(merge them into an existing `[tools]` table rather than duplicating it):

```toml
[tools]
go = "1.27.2"
"go:github.com/api-mittens/looster/cmd/looster" = "latest"
```

After reviewing a checked-out config, install its tools and run Looster:

```sh
mise trust
mise install
mise exec -- looster --help
mise exec -- looster --config config.toml task ls
```

Commit `mise.toml` to share the setup with collaborators. `latest` is convenient
for getting started. For reproducible team installs, replace it with a published
Looster release version (for example, `v0.1.0` if that tag exists). Keep the Go
version compatible with the selected release's `go.mod`. To keep the setup only
on your machine, put the same entries in a gitignored `mise.local.toml` instead.

`mise exec --` works without shell activation. With
[mise shell activation](https://mise.jdx.dev/cli/activate.html), you can run
`looster` directly while in the configured repository. Continue with the
[quick start](../readme.md#quick-start), prefixing its Looster commands with
`mise exec --` when not using activation. Mise manages the executable only:
PostgreSQL, S3 configuration, and a separately authenticated Jcode installation
are still required to run workers.

## Build from source

The module is `github.com/api-mittens/looster`. Use **Go 1.27.2 or newer**, as declared
in [go.mod](../go.mod), plus a C/C++ compiler for the embedded DuckDB engine.
The official client links its bundled static library by default. Do not use
`CGO_ENABLED=0` or require a separately installed DuckDB executable. From the
source checkout:

```sh
CGO_ENABLED=1 go build -buildvcs=false -o bin/looster ./cmd/looster
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
