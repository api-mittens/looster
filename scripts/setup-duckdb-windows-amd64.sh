#!/usr/bin/env bash
# Match DuckDB v1.5.2's BundleStaticLibs.yml: Chocolatey mingw 14.2.0.
# Its exact package pins this niXman POSIX/SEH/UCRT archive and SHA-256.
set -euo pipefail
archive_name=x86_64-14.2.0-release-posix-seh-ucrt-rt_v12-rev0.7z
checksum=0f1afc3b48f66dda68fbfb7b8b0f1d22b831396fbe1e3dea776745f32d930b24
install=$(cygpath -u "${RUNNER_TEMP:?}")/duckdb-mingw14
mkdir -p "$install"
archive="$install/$archive_name"
curl --fail --location --retry 3 "https://github.com/niXman/mingw-builds-binaries/releases/download/14.2.0-rt_v12-rev0/$archive_name" -o "$archive"
printf '%s  %s\n' "$checksum" "$archive" | sha256sum --check --strict
sevenzip=$(command -v 7z || command -v 7z.exe || true)
if [[ -z "$sevenzip" ]]; then
  sevenzip=$(cygpath -u "${PROGRAMFILES:-${ProgramFiles:-C:/Program Files}}/7-Zip/7z.exe")
fi
"$sevenzip" x -y "-o$install" "$archive" >/dev/null
compiler_root="$install/mingw64"
cc="$compiler_root/bin/gcc.exe"
[[ $("$cc" -dumpfullversion) == 14.2.0 ]] || { echo 'Incorrect DuckDB compiler version' >&2; exit 1; }
[[ $("$cc" -dumpmachine) == x86_64-w64-mingw32 ]] || { echo 'Incorrect DuckDB compiler architecture' >&2; exit 1; }
# GCC 16 changed this TLS runtime ABI. Verify genuine runtime definitions before
# attempting the application link, never synthesize compatibility symbols.
libstdcxx=$(cygpath -u "$("$cc" -print-file-name=libstdc++.a)")
"$compiler_root/bin/nm.exe" --defined-only "$libstdcxx" > "$install/libstdcxx-defined.txt"
for symbol in __emutls_v._ZSt11__once_call __emutls_v._ZSt15__once_callable; do
  grep -F " $symbol" "$install/libstdcxx-defined.txt" >/dev/null || { echo "Pinned runtime lacks $symbol" >&2; exit 1; }
done
for name in gcc mingw-w64 winpthreads; do
  test -d "$compiler_root/licenses/$name"
done
test -s "$compiler_root/licenses/gcc/COPYING.RUNTIME"
{
  echo "CC=$(cygpath -m "$cc")"
  echo "CXX=$(cygpath -m "$compiler_root/bin/g++.exe")"
  echo "OBJDUMP=$(cygpath -m "$compiler_root/bin/objdump.exe")"
  echo "LOOSTER_MINGW_ROOT=$(cygpath -m "$compiler_root")"
  echo 'CGO_LDFLAGS=-static -static-libgcc'
} >> "${GITHUB_ENV:?}"
printf '%s\n' "$(cygpath -m "$compiler_root/bin")" >> "${GITHUB_PATH:?}"
"$cc" --version
