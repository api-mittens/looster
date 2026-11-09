#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d "${JCODE_SCRATCH_DIR:-${TMPDIR:-/tmp}}/runtime-notices.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/tools" "$tmp/stage" "$tmp/fixtures"
real_cp=$(command -v cp)
export RUNTIME_NOTICE_REAL_CP="$real_cp" RUNTIME_NOTICE_FIXTURES="$tmp/fixtures"
printf 'fixture GCC copyright including runtime exception\n' > "$tmp/fixtures/copyright"
printf 'fixture GNU GPL version 3\n' > "$tmp/fixtures/GPL-3"
cat > "$tmp/tools/g++" <<'MOCK'
#!/usr/bin/env bash
case "$1" in
  -dumpversion) echo 14.2.0 ;;
  --version) echo 'fixture g++ 14.2.0' ;;
  *) exit 1 ;;
esac
MOCK
cat > "$tmp/tools/cp" <<'MOCK'
#!/usr/bin/env bash
if [[ $1 == -L ]]; then shift; fi
case "$1" in
  /usr/share/doc/gcc-14-base/copyright) source="$RUNTIME_NOTICE_FIXTURES/copyright" ;;
  /usr/share/common-licenses/GPL-3) source="$RUNTIME_NOTICE_FIXTURES/GPL-3" ;;
  *) echo "unexpected runtime notice source: $1" >&2; exit 1 ;;
esac
"$RUNTIME_NOTICE_REAL_CP" "$source" "$2"
MOCK
chmod +x "$tmp/tools/"*
export PATH="$tmp/tools:$PATH"
for arch in amd64 arm64; do
  stage="$tmp/linux-$arch"
  bash "$root/scripts/package-runtime-notices.sh" linux "$arch" "$stage"
  cmp "$tmp/fixtures/copyright" "$stage/NATIVE_RUNTIME_LICENSES/GCC-copyright"
  cmp "$tmp/fixtures/GPL-3" "$stage/NATIVE_RUNTIME_LICENSES/GPL-3"
  grep -Fx 'fixture g++ 14.2.0' "$stage/NATIVE_RUNTIME_LICENSES/compiler-version.txt" > /dev/null
  bash "$root/scripts/package-runtime-notices.sh" darwin "$arch" "$tmp/darwin-$arch"
  [[ ! -e "$tmp/darwin-$arch/NATIVE_RUNTIME_LICENSES" ]]
done
rm "$tmp/fixtures/copyright"
if bash "$root/scripts/package-runtime-notices.sh" linux amd64 "$tmp/missing" > "$tmp/output" 2>&1; then
  echo 'missing GCC notice was accepted' >&2; exit 1
fi
if bash "$root/scripts/package-runtime-notices.sh" linux riscv64 "$tmp/unsupported" > "$tmp/output" 2>&1; then
  echo 'unsupported runtime license target was accepted' >&2; exit 1
fi
echo 'runtime notice packaging: all cases passed'
