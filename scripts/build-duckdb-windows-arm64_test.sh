#!/usr/bin/env bash
# Exercise the complete ARM recipe without requiring a Windows compiler.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d "${JCODE_SCRATCH_DIR:-${TMPDIR:-/tmp}}/arm-static.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/tools" "$tmp/runner"
export RUNNER_TEMP="$tmp/runner" GITHUB_ENV="$tmp/env" ARM_TEST_LOG="$tmp/cmake" ARM_TEST_MRI="$tmp/mri"
cat > "$tmp/tools/go" <<'MOCK'
#!/usr/bin/env bash
case "$*" in
  *github.com/duckdb/duckdb-go/v2) echo v2.10502.0 ;;
  *github.com/duckdb/duckdb-go-bindings) echo v0.10502.0 ;;
  *) exit 1 ;;
esac
MOCK
cat > "$tmp/tools/git" <<'MOCK'
#!/usr/bin/env bash
exit 0
MOCK
cat > "$tmp/tools/cygpath" <<'MOCK'
#!/usr/bin/env bash
printf '%s\n' "$2"
MOCK
cat > "$tmp/tools/cmake" <<'MOCK'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$ARM_TEST_LOG"
if [[ $1 == --build ]]; then
  [[ $* == *'--target duckdb_static duckdb_generated_extension_loader icu_extension json_extension parquet_extension autocomplete_extension tpch_extension tpcds_extension core_functions_extension' ]] || exit 1
  mkdir -p "$2/src" "$2/extension" "$2/third_party"
  for archive in src/libduckdb_static.a extension/libduckdb_generated_extension_loader.a extension/libdummy_static_extension_loader.a third_party/libdependency.a; do
    printf fixture > "$2/$archive"
  done
fi
MOCK
cat > "$tmp/tools/llvm-ar" <<'MOCK'
#!/usr/bin/env bash
cat > "$ARM_TEST_MRI"
while IFS= read -r line; do
  case "$line" in CREATE\ *) printf bundled > "${line#CREATE }" ;; esac
done < "$ARM_TEST_MRI"
MOCK
chmod +x "$tmp/tools/"*
PATH="$tmp/tools:$PATH" bash "$root/scripts/build-duckdb-windows-arm64.sh"
[[ $(wc -l < "$ARM_TEST_LOG" | tr -d ' ') == 2 ]]
if grep -F DUCKDB_EXPLICIT_PLATFORM "$ARM_TEST_LOG" > /dev/null; then
  echo 'ARM compiler ABI was overridden' >&2
  exit 1
fi
for archive in libduckdb_static.a libduckdb_generated_extension_loader.a libdependency.a; do
  grep -F "/$archive" "$ARM_TEST_MRI" > /dev/null
done
if grep -F dummy_static_extension_loader "$ARM_TEST_MRI" > /dev/null; then
  echo 'dummy extension loader was included in the native bundle' >&2
  exit 1
fi
grep -Fx DUCKDB_BUILD_TAGS=duckdb_use_static_lib "$GITHUB_ENV" > /dev/null
echo 'Windows ARM static target and bundle checks: all cases passed'
