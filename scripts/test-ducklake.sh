#!/bin/sh
# Run from any directory. Requires Go, CGo toolchain and PostgreSQL 17 tools.
set -eu
cd "$(dirname "$0")/.."
: "${JCODE_SCRATCH_DIR:=${TMPDIR:-/tmp}/looster-test-scratch}"
export JCODE_SCRATCH_DIR
mkdir -p "$JCODE_SCRATCH_DIR"
runner=$(mktemp -d "$JCODE_SCRATCH_DIR/ducklake-runner.XXXXXX")
pid=
cleanup() {
    if [ -n "$pid" ]; then
        kill -TERM "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
    fi
    rm -rf "$runner"
}
trap cleanup 0
trap 'exit 130' INT
trap 'exit 143' TERM
# A real binary, rather than go run, ensures signals reach the cleanup owner.
go build -o "$runner/harness" ./scripts/test-ducklake
"$runner/harness" &
pid=$!
code=0
wait "$pid" || code=$?
pid=
exit "$code"
