#!/usr/bin/env bash
# Validate the complete published target set before checksumming archive bytes.
set -euo pipefail
dir=${1:?usage: check-release-archives.sh DIRECTORY VERSION}
version=${2:?}
for target in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
  test -s "$dir/looster_${version}_${target}.tar.gz" || { echo "Missing release archive: $target" >&2; exit 1; }
done
if [[ $(find "$dir" -maxdepth 1 -type f | wc -l) -ne 4 ]]; then
  echo 'Expected exactly four release archives and no other files' >&2
  exit 1
fi
if command -v sha256sum >/dev/null; then
  (cd "$dir" && sha256sum *.tar.gz > checksums.txt)
else
  (cd "$dir" && shasum -a 256 *.tar.gz > checksums.txt)
fi
