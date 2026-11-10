#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d "${JCODE_SCRATCH_DIR:-${TMPDIR:-/tmp}}/static-cxx.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/runtime with spaces"
touch "$tmp/runtime with spaces/libstdc++.a"
cat > "$tmp/compiler" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == -print-file-name=libstdc++.a ]]; then
  printf '%s\n' "$MOCK_ARCHIVE"
else
  printf '%s\n' "$@" > "$MOCK_ARGS"
fi
MOCK
chmod +x "$tmp/compiler"
export MOCK_ARCHIVE="$tmp/runtime with spaces/libstdc++.a" MOCK_ARGS="$tmp/args"
LOOSTER_REAL_CC="$tmp/compiler" bash "$root/scripts/static-cxx-cc.sh" -o 'output with spaces' -lstdc++ -lm -lstdc++
printf '%s\n' -o 'output with spaces' "$MOCK_ARCHIVE" -lm "$MOCK_ARCHIVE" > "$tmp/expected"
cmp "$tmp/expected" "$MOCK_ARGS"
LOOSTER_REAL_CC="$tmp/compiler" bash "$root/scripts/static-cxx-cc.sh" -E -dM -xc -
printf '%s\n' -E -dM -xc - > "$tmp/expected"
cmp "$tmp/expected" "$MOCK_ARGS"
if MOCK_ARCHIVE=libstdc++.a LOOSTER_REAL_CC="$tmp/compiler" bash "$root/scripts/static-cxx-cc.sh" -lstdc++ 2> "$tmp/error"; then
  echo 'missing static archive accepted' >&2; exit 1
fi
if LOOSTER_REAL_CC="$tmp/compiler" bash "$root/scripts/static-cxx-cc.sh" @response 2> "$tmp/error"; then
  echo 'unreviewed response file accepted' >&2; exit 1
fi
if [[ $(uname -s) == Linux ]]; then
  # Exercise the exact explicit-link failure from CI using the real toolchain.
  cat > "$tmp/main.cpp" <<'CPP'
#include <iostream>
int main() { std::cout << 42 << std::endl; }
CPP
  g++ -c "$tmp/main.cpp" -o "$tmp/main.o"
  LOOSTER_REAL_CC=gcc bash "$root/scripts/static-cxx-cc.sh" "$tmp/main.o" -lstdc++ -lm -static-libgcc -o "$tmp/native"
  [[ $("$tmp/native") == 42 ]] || exit 1
  if readelf -d "$tmp/native" | grep -E 'NEEDED.*(libstdc\+\+|libgcc_s)'; then
    echo 'native smoke binary retained a dynamic C++ runtime' >&2; exit 1
  fi
fi
echo 'explicit static C++ linker: all cases passed'
