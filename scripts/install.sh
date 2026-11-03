#!/bin/sh
# Install a checksum-verified Looster release without root or a Go toolchain.
set -eu

fail() { printf 'looster: %s\n' "$*" >&2; exit 1; }
repo=${LOOSTER_REPO:-api-mittens/looster}
install_dir=${LOOSTER_INSTALL_DIR:-"$HOME/.local/bin"}
version=${LOOSTER_VERSION:-}
case "$repo" in *[!A-Za-z0-9_./-]*|*..*|/*|*/|*/*/*) fail 'Invalid LOOSTER_REPO (expected owner/repository)' ;; esac
case "$repo" in */*) ;; *) fail 'LOOSTER_REPO must be owner/repository' ;; esac
for command in curl tar uname mktemp chmod mkdir mv awk; do
  command -v "$command" >/dev/null 2>&1 || fail "Required command not found: $command"
done
if command -v sha256sum >/dev/null 2>&1; then
  checksum=sha256sum
elif command -v shasum >/dev/null 2>&1; then
  checksum=shasum
else
  fail 'Install sha256sum or shasum to verify the download'
fi
case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail 'This installer supports Linux and macOS. Download Windows binaries from GitHub Releases.' ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) fail 'Unsupported architecture (expected amd64 or arm64)' ;;
esac
if [ -z "$version" ]; then
  latest=$(curl --proto '=https' --tlsv1.2 -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest")
  version=${latest##*/}
fi
case "$version" in v[0-9]*.[0-9]*.[0-9]*) ;; *) fail 'LOOSTER_VERSION must be a v-prefixed version, such as v0.1.0' ;; esac
case "$version" in *[!A-Za-z0-9.+-]*) fail 'Invalid release version' ;; esac
archive="looster_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$repo/releases/download/$version"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
printf 'Downloading Looster %s (%s/%s)...\n' "$version" "$os" "$arch"
curl --proto '=https' --tlsv1.2 -fsSL "$base/$archive" -o "$tmp/$archive"
curl --proto '=https' --tlsv1.2 -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
expected=$(awk -v name="$archive" '$2 == name { print $1; count++ } END { if (count != 1) exit 1 }' "$tmp/checksums.txt") || fail 'Release checksum is missing or ambiguous'
if [ "$checksum" = sha256sum ]; then
  actual=$(sha256sum "$tmp/$archive")
else
  actual=$(shasum -a 256 "$tmp/$archive")
fi
actual=${actual%% *}
[ "$actual" = "$expected" ] || fail 'Checksum verification failed. Nothing was installed.'
# Extract only the executable, never arbitrary paths from the archive.
tar -xzf "$tmp/$archive" -C "$tmp" ./looster
[ -f "$tmp/looster" ] && [ ! -L "$tmp/looster" ] || fail 'Release does not contain a regular looster executable'
mkdir -p "$install_dir"
# Stage on the destination filesystem, then replace atomically.
staged=$(mktemp "$install_dir/.looster.XXXXXX")
trap 'rm -rf "$tmp"; rm -f "$staged"' EXIT HUP INT TERM
cat "$tmp/looster" > "$staged"
chmod 755 "$staged"
mv -f "$staged" "$install_dir/looster"
printf 'Installed %s/looster\n' "$install_dir"
case ":${PATH:-}:" in
  *":$install_dir:"*) ;;
  *) printf 'Add it to your PATH: export PATH="%s:$PATH"\n' "$install_dir" ;;
esac
