#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d "${JCODE_SCRATCH_DIR:-${TMPDIR:-/tmp}}/native-deps.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/tools"
cat > "$tmp/tools/go" <<'MOCK'
#!/usr/bin/env bash
printf '\tdep\tgithub.com/duckdb/duckdb-go/v2\tv2.10502.0\n'
MOCK
for tool in readelf otool; do
  cat > "$tmp/tools/$tool" <<'MOCK'
#!/usr/bin/env bash
printf '%s\n' "$MOCK_DEPS"
MOCK
  chmod +x "$tmp/tools/$tool"
done
chmod +x "$tmp/tools/go"
export PATH="$tmp/tools:$PATH"
expect_pass() {
  MOCK_DEPS="$2" bash "$root/scripts/check-native-deps.sh" fake "$1" > "$tmp/output" 2>&1 || { cat "$tmp/output"; exit 1; }
}
expect_fail() {
  if MOCK_DEPS="$2" bash "$root/scripts/check-native-deps.sh" fake "$1" > "$tmp/output" 2>&1; then
    echo "unexpected pass for $1: $2" >&2; exit 1
  fi
}
expect_pass linux '0x0000000000000001 (NEEDED) Shared library: [libc.so.6]'
expect_fail linux '0x0000000000000001 (NEEDED) Shared library: [libduckdb.so]'
expect_fail linux '0x0000000000000001 (NEEDED) Shared library: [libstdc++.so.6]'
expect_pass darwin $'fake:\n\t/usr/lib/libc++.1.dylib (compatibility version 1.0.0)'
expect_fail darwin $'fake:\n\t@rpath/libduckdb.dylib (compatibility version 1.0.0)'
expect_fail darwin $'fake:\n\t/opt/homebrew/lib/libduckdb.dylib (compatibility version 1.0.0)'
expect_fail freebsd ''
echo 'native dependency checks: all cases passed'
