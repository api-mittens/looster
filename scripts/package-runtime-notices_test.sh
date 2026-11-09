#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d "${JCODE_SCRATCH_DIR:-${TMPDIR:-/tmp}}/runtime-notices.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/tools" "$tmp/stage"
for compiler in clang gcc; do
  cat > "$tmp/tools/$compiler" <<'MOCK'
#!/usr/bin/env bash
echo 'fixture compiler version'
MOCK
  chmod +x "$tmp/tools/$compiler"
done
export PATH="$tmp/tools:$PATH" MINGW_PREFIX="$tmp/prefix"
for name in 'libc++' libunwind compiler-rt; do
  mkdir -p "$MINGW_PREFIX/share/licenses/$name"
  echo "fixture license for $name" > "$MINGW_PREFIX/share/licenses/$name/LICENSE"
done
bash "$root/scripts/package-runtime-notices.sh" windows arm64 "$tmp/stage"
for name in 'libc++' libunwind compiler-rt; do
  cmp "$MINGW_PREFIX/share/licenses/$name/LICENSE" "$tmp/stage/NATIVE_RUNTIME_LICENSES/$name/LICENSE"
done
rm "$MINGW_PREFIX/share/licenses/compiler-rt/LICENSE"
if bash "$root/scripts/package-runtime-notices.sh" windows arm64 "$tmp/stage"; then
  echo 'missing LLVM license was accepted' >&2; exit 1
fi
mkdir -p "$MINGW_PREFIX/share/licenses/gcc"
echo 'fixture runtime exception' > "$MINGW_PREFIX/share/licenses/gcc/COPYING.RUNTIME"
bash "$root/scripts/package-runtime-notices.sh" windows amd64 "$tmp/stage"
cmp "$MINGW_PREFIX/share/licenses/gcc/COPYING.RUNTIME" "$tmp/stage/NATIVE_RUNTIME_LICENSES/gcc/COPYING.RUNTIME"
rm "$MINGW_PREFIX/share/licenses/gcc/COPYING.RUNTIME"
if bash "$root/scripts/package-runtime-notices.sh" windows amd64 "$tmp/stage"; then
  echo 'missing GCC runtime exception was accepted' >&2; exit 1
fi
echo 'runtime notice packaging: all cases passed'
