#!/usr/bin/env bash
# Preserve the actual compiler runtime notices alongside each static artifact.
set -euo pipefail
os=${1:?usage: package-runtime-notices.sh OS ARCH STAGE}
arch=${2:?}
stage=${3:?}
dest="$stage/NATIVE_RUNTIME_LICENSES"
case "$os/$arch" in
  linux/amd64|linux/arm64)
    mkdir -p "$dest"
    major=$(g++ -dumpversion)
    major=${major%%.*}
    cp -L "/usr/share/doc/gcc-$major-base/copyright" "$dest/GCC-copyright"
    cp /usr/share/common-licenses/GPL-3 "$dest/GPL-3"
    g++ --version > "$dest/compiler-version.txt"
    ;;
  darwin/amd64|darwin/arm64)
    # Apple's C++ runtime is an OS library, not redistributed in the archive.
    ;;
  *) echo 'Unsupported runtime license platform' >&2; exit 1 ;;
esac
