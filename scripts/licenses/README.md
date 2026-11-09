# Native release licensing inventory

`go run ./scripts/licenses` inventories both test and release build graphs for
all four supported targets: Linux and macOS, each on amd64 and arm64, with cgo
enabled. Platform-specific test memberships must not depend on the machine that
generated the notices. `-check` compares the generated aggregate with
`THIRD_PARTY_NOTICES.md`. Windows is not a supported release target and its
platform-specific bindings are not part of the published binaries' inventory.
The shared driver and bindings module licenses remain included wherever used.

The native engine is not just the Go driver module. `duckdb-notices.md` retains
DuckDB's own license and recursively discovered notices from its vendored
`third_party` and `extension` directories. This intentionally conservative
inventory can include source components that the linker discards. It includes
ICU's complete bundled collection and the TPC EULA, rather than describing all
native code as MIT. The TPC legend is prominent in the separately packaged
`NATIVE_ENGINE_NOTICES.md`. Review distribution restrictions before charging for
or changing distribution of the TPC components.

## Updating the native engine

1. Review the official driver's version matrix and static linking documentation:
   `github.com/duckdb/duckdb-go` at tag `v2.10502.0`, README sections
   "Version Support" and "Linking a Static Library".
2. Match driver `v2.10502.0`, bindings `v0.10502.0`, DuckDB `v1.5.2`, and source
   commit `8a5851971fae891f292c2714d86046ee018e9737`. Review every changed native
   notice and update the pins in `native.go`.
3. Check out that exact DuckDB commit in a clean source checkout. Run
   `go run ./scripts/licenses -duckdb-source /path/to/duckdb`. The command rejects
   a different commit or tracked modifications. Unknown licenses fail closed.
4. Review the resulting snapshot, update its SHA-256 in `native.go`, then run
   `go run ./scripts/licenses` and `go run ./scripts/licenses -check`.
5. Run `go test ./scripts/licenses`, the shell regression suites, and all four
   native CI build jobs. A local macOS ARM64 build validates only that target.

All four targets use the official version-pinned prebuilt static archives.
Each Linux archive that embeds a compiler runtime also carries its actual
runner's `NATIVE_RUNTIME_LICENSES` directory, including GCC copyright, GPL text
and the runtime exception. macOS uses the system C++ runtime, not a separately
redistributed library. These notices are additional to the source-pinned DuckDB
inventory.

Only operating-system native dependencies are accepted in release executables.
Linux uses `static-cxx-cc.sh` to replace upstream's explicit `-lstdc++` with the
compiler's static archive. GCC's `-static-libstdc++` alone does not replace that
explicit library argument. DuckDB shared objects and separately installed
C++/pthread runtimes fail artifact inspection. This means the engine is
statically embedded, not that the entire executable is independent of its
platform C runtime or system frameworks.
Runtime-downloaded DuckDB extensions are not shipped inside these archives and
are outside this release artifact's source inventory.
