#!/usr/bin/env bash
set -euo pipefail
os=${1:?usage: release-archive.sh OS ARCH VERSION}
arch=${2:?}
version=${3:?}
[[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]] || { echo 'Expected v-prefixed semantic version' >&2; exit 1; }
case "$os/$arch" in
  linux/amd64|linux/arm64|darwin/amd64|darwin/arm64) ;;
  *) echo 'Unsupported release platform' >&2; exit 1 ;;
esac
[[ "$(go env GOHOSTOS)/$(go env GOHOSTARCH)" == "$os/$arch" ]] || { echo 'Release builds must run on the matching native runner' >&2; exit 1; }
export CGO_ENABLED=1 GOOS="$os" GOARCH="$arch"
name="looster_${version}_${os}_${arch}"
stage="dist/$name"
mkdir -p "$stage"
binary=looster
go build -trimpath \
  -ldflags="-s -w -X github.com/api-mittens/looster/internal/cli.Version=$version" \
  -o "$stage/$binary" ./cmd/looster
bash scripts/check-native-deps.sh "$stage/$binary" "$os"
"$stage/$binary" --help
# These tests open and query the embedded engine without an external CLI.
LOOSTER_TEST_DUCKLAKE_EXTENSIONS=yes go test -count=1 -timeout=10m ./internal/ducklake
cp LICENSE THIRD_PARTY_NOTICES.md logo.svg readme.md config.example.toml behaviour.example behaviour_example.txt "$stage/"
mkdir -p "$stage/third_party_sources" "$stage/scripts"
hcl_version=$(go list -m -f '{{.Version}}' github.com/hashicorp/hcl)
go mod download "github.com/hashicorp/hcl@$hcl_version"
cache=$(go env GOMODCACHE)
cp "$cache/cache/download/github.com/hashicorp/hcl/@v/$hcl_version.zip" "$stage/third_party_sources/hashicorp-hcl-$hcl_version.zip"
cp scripts/licenses/duckdb-notices.md "$stage/NATIVE_ENGINE_NOTICES.md"
bash scripts/package-runtime-notices.sh "$os" "$arch" "$stage"
cp -R docs "$stage/"
cp scripts/install.sh "$stage/scripts/"
tar -czf "dist/$name.tar.gz" -C "$stage" .
rm -r "$stage"
