#!/usr/bin/env bash
# Preserve the actual compiler runtime notices alongside each static artifact.
set -euo pipefail
os=${1:?usage: package-runtime-notices.sh OS ARCH STAGE}
arch=${2:?}
stage=${3:?}
dest="$stage/NATIVE_RUNTIME_LICENSES"
case "$os/$arch" in
  linux/*)
    mkdir -p "$dest"
    major=$(g++ -dumpversion)
    major=${major%%.*}
    cp -L "/usr/share/doc/gcc-$major-base/copyright" "$dest/GCC-copyright"
    cp /usr/share/common-licenses/GPL-3 "$dest/GPL-3"
    g++ --version > "$dest/compiler-version.txt"
    ;;
  windows/arm64)
    mkdir -p "$dest"
    for name in 'libc++' libunwind compiler-rt; do
      test -d "${MINGW_PREFIX:?}/share/licenses/$name"
      [[ -n "$(find "$MINGW_PREFIX/share/licenses/$name" -type f -print -quit)" ]] || { echo "Missing runtime license: $name" >&2; exit 1; }
      cp -RL "$MINGW_PREFIX/share/licenses/$name" "$dest/"
    done
    clang --version > "$dest/compiler-version.txt"
    ;;
  windows/amd64)
    mkdir -p "$dest"
    test -s "${MINGW_PREFIX:?}/share/licenses/gcc/COPYING.RUNTIME"
    for name in gcc libgcc 'libstdc++' winpthreads libwinpthread crt headers mingw-w64-crt mingw-w64-headers; do
      if [[ -d "$MINGW_PREFIX/share/licenses/$name" ]]; then
        cp -RL "$MINGW_PREFIX/share/licenses/$name" "$dest/"
      fi
    done
    gcc --version > "$dest/compiler-version.txt"
    ;;
  darwin/*)
    # Apple's C++ runtime is an OS library, not redistributed in the archive.
    ;;
  *) echo 'Unsupported runtime license platform' >&2; exit 1 ;;
esac
