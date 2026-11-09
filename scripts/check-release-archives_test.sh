#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d "${JCODE_SCRATCH_DIR:-${TMPDIR:-/tmp}}/release-archives.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
version=v0.0.0-fixture
mkdir "$tmp/dist"
for target in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
  printf 'archive fixture %s\n' "$target" > "$tmp/dist/looster_${version}_${target}.tar.gz"
done
bash "$root/scripts/check-release-archives.sh" "$tmp/dist" "$version"
[[ $(wc -l < "$tmp/dist/checksums.txt") -eq 4 ]] || { echo 'wrong checksum count' >&2; exit 1; }
if command -v sha256sum >/dev/null; then
  (cd "$tmp/dist" && sha256sum -c checksums.txt)
else
  (cd "$tmp/dist" && shasum -a 256 -c checksums.txt)
fi
rm "$tmp/dist/checksums.txt"
expect_failure() {
  if bash "$root/scripts/check-release-archives.sh" "$tmp/dist" "$version" > "$tmp/output" 2>&1; then
    echo "invalid archive set accepted: $1" >&2; exit 1
  fi
  [[ ! -e "$tmp/dist/checksums.txt" ]] || { echo 'invalid set was checksummed' >&2; exit 1; }
}
printf 'extra fixture' > "$tmp/dist/unexpected.zip"
expect_failure extra
rm "$tmp/dist/unexpected.zip"
archive="$tmp/dist/looster_${version}_darwin_arm64.tar.gz"
rm "$archive"
expect_failure missing
: > "$archive"
expect_failure empty
echo 'release archive set and checksum checks: all cases passed'
