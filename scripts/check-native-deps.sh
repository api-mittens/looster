#!/usr/bin/env bash
# Verify the native engine is embedded, not a runtime installation prerequisite.
set -euo pipefail
binary=${1:?usage: check-native-deps.sh BINARY [OS]}
os=${2:-$(go env GOOS)}
case "$os" in
  linux)
    dependencies=$(readelf -d "$binary")
    printf '%s\n' "$dependencies"
    # Only the platform C runtime is permitted. C++ must be linked statically.
    unexpected=$(printf '%s\n' "$dependencies" | sed -n 's/.*Shared library: \[\(.*\)\].*/\1/p' | grep -Ev '^(libc|libm|libpthread|libdl|librt|libresolv)\.so(\.[0-9]+)*$|^ld-linux[^/]*\.so(\.[0-9]+)*$' || true)
    ;;
  darwin)
    dependencies=$(otool -L "$binary")
    printf '%s\n' "$dependencies"
    unexpected=$(printf '%s\n' "$dependencies" | tail -n +2 | awk '{print $1}' | grep -Ev '^(/usr/lib/|/System/Library/)' || true)
    ;;
  windows)
    dependencies=$("${OBJDUMP:-objdump}" -p "$binary")
    printf '%s\n' "$dependencies"
    unexpected=$(printf '%s\n' "$dependencies" | awk '/DLL Name:/ {print tolower($3)}' | grep -Ev '^(api-ms-win-.*|kernel32|ntdll|user32|advapi32|rstrtmgr|ws2_32|wsock32|bcrypt|crypt32|secur32|shell32|ole32|oleaut32|version|shlwapi|msvcrt|ucrtbase|iphlpapi|userenv|normaliz|setupapi|winmm|gdi32)\.dll$' || true)
    ;;
  *) echo "Unsupported OS: $os" >&2; exit 1 ;;
esac
if [[ -n "$unexpected" ]]; then
  printf 'Non-system native runtime dependencies are forbidden:\n%s\n' "$unexpected" >&2
  exit 1
fi
# The module build info proves the executable includes the selected Go driver.
go version -m "$binary" | grep -F 'github.com/duckdb/duckdb-go/v2'
