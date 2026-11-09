# Native release licensing inventory

`go run ./scripts/licenses` inventories both test and release build graphs for
all six platforms with cgo enabled. Platform-specific test memberships must not
depend on the machine that generated the notices. Windows ARM64 uses `duckdb_use_static_lib`, not a
nonexistent upstream prebuilt archive. `-check` compares the generated aggregate
with `THIRD_PARTY_NOTICES.md`.

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
   notice and update the pins in `native.go` and the Windows ARM64 build script.
3. Check out that exact DuckDB commit in a clean source checkout. Run
   `go run ./scripts/licenses -duckdb-source /path/to/duckdb`. The command rejects
   a different commit or tracked modifications. Unknown licenses fail closed.
4. Review the resulting snapshot, update its SHA-256 in `native.go`, then run
   `go run ./scripts/licenses` and `go run ./scripts/licenses -check`.
5. Run `go test ./scripts/licenses`, `bash scripts/check-native-deps_test.sh`, and
   all six CI build jobs. A local macOS build does not validate Windows ARM64.

The Windows ARM64 recipe follows the official custom-static-library approach.
It includes the generated extension-loader archive added in DuckDB 1.5, plus the
same benchmark extensions referenced by the five official archive link lists.
MSYS2 CLANGARM64 supplies the native GNU-ABI compiler and static C++ runtime.
Each archive that embeds a compiler runtime also carries its actual runner's
`NATIVE_RUNTIME_LICENSES` directory. Linux retains GCC copyright and GPL text
including the runtime exception. Windows retains GCC runtime exception notices
or LLVM's libc++, libunwind and compiler-rt Apache/LLVM-exception notices.
These runtime notices are additional to the source-pinned DuckDB inventory.

Only operating-system native dependencies are accepted in release executables.
Linux uses `static-cxx-cc.sh` to replace upstream's explicit `-lstdc++` with the
compiler's static archive. GCC's `-static-libstdc++` alone does not replace that
explicit library argument. Windows AMD64 uses the exact GCC 14.2.0 POSIX/SEH/UCRT
compiler pinned by DuckDB v1.5.2's `.github/workflows/BundleStaticLibs.yml`.
`setup-duckdb-windows-amd64.sh` verifies the niXman archive SHA-256 published in
Chocolatey mingw 14.2.0's installer and checks genuine static definitions of both
`std::call_once` emulated-TLS symbols. Current MSYS2 GCC 16's static C++ runtime
is not ABI-compatible with those prebuilt archives. No symbol shims are used.
The release packages GCC, MinGW-w64 and winpthreads notices from that exact
compiler archive, not from MSYS2's independently updated toolchain. Upstream's
`--static` link directive remains enabled.
DuckDB DLLs/shared objects and separately installed C++/pthread runtimes fail the
artifact inspection. This means the engine is statically embedded, not that the
entire executable is independent of its platform C runtime or system frameworks.
Runtime-downloaded DuckDB extensions are not shipped inside these archives and
are outside this release artifact's source inventory.
