#!/usr/bin/env bash
# Real-tool declaration regressions. Only disposable repositories are modified.
set -euo pipefail
source_hook=$(cd "$(dirname "$0")" && pwd)/pre-commit
scratch=$(mktemp -d "${JCODE_SCRATCH_DIR:-${TMPDIR:-/tmp}}/looster-declarations.XXXXXXXX")
trap 'rm -rf -- "$scratch"' EXIT
mkdir "$scratch/scripts"
cp "$source_hook" "$scratch/scripts/pre-commit"
if [[ -d $(dirname "$source_hook")/lint-audit ]]; then
  cp -R "$(dirname "$source_hook")/lint-audit" "$scratch/scripts/lint-audit"
fi
hook=$scratch/scripts/pre-commit
linter=$(command -v golangci-lint)
go_bin=$(command -v go)
case "$linter" in */mise/shims/*) linter=$(mise which golangci-lint) ;; esac
case "$go_bin" in */mise/shims/*) go_bin=$(mise which go) ;; esac
export PATH="$(dirname "$go_bin"):$(dirname "$linter"):$PATH"
[[ $(go version) == 'go version go1.27.2 '* ]] || { echo 'Requires Go 1.27.2' >&2; exit 1; }
[[ $("$linter" version) == *'version 2.15.0 '* ]] || { echo 'Requires golangci-lint 2.15.0' >&2; exit 1; }
while IFS= read -r name; do unset "$name"; done < <(git rev-parse --local-env-vars)
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null
started=$SECONDS
count=0
failed=0
fixture() {
  count=$((count + 1))
  mkdir "$scratch/repo-$count"
  cd "$scratch/repo-$count"
  git init -q
  git config user.name 'Declaration regression'
  git config user.email 'fixture@example.invalid'
  if [[ -d $scratch/scripts/lint-audit ]]; then
    mkdir scripts
    cp -R "$scratch/scripts/lint-audit" scripts/lint-audit
  fi
  printf 'module example.invalid/declarations\n\ngo 1.27.2\n' > go.mod
  cat > .golangci.yml <<YAML
version: "2"
linters:
  default: none
  enable: [$1]
  settings:
    gocyclo:
      min-complexity: 30
issues:
  max-issues-per-linter: 0
  max-same-issues: 0
YAML
  cat > fixture_test.go <<'GO'
package fixture

import "testing"

func TestSnapshot(t *testing.T) {
	t.Parallel()
}
GO
}
complexity() {
  {
    printf 'package fixture\n\nfunc Complexity(n int) int {\n'
    for ((i=1; i<=$1; i++)); do printf '\tif n == %s { n++ }\n' "$i"; done
    printf '\treturn n\n}\n'
  } > fixture.go
  gofmt -w fixture.go
}
commit_baseline() {
  git add .
  git -c core.hooksPath=/dev/null commit -qm baseline
}
# Assert the real diagnostic, including the unchanged declaration location.
full_lint() {
  local expected=$1 location=${2:-}
  local status=0
  "$linter" run ./... > "$scratch/full-$count.log" 2>&1 || status=$?
  if [[ $expected == clean ]]; then
    if ((status != 0)); then cat "$scratch/full-$count.log"; exit 1; fi
  else
    if ((status != 1)) || ! grep -Fq "($expected)" "$scratch/full-$count.log" || ! grep -Eq "$location" "$scratch/full-$count.log"; then
      echo "Unexpected real diagnostic for case $count" >&2
      cat "$scratch/full-$count.log" >&2
      exit 1
    fi
    cat "$scratch/full-$count.log"
  fi
}
check_hook() {
  local expected=$1 label=$2 diagnostic=${3:-}
  git add .
  local before_tree
  before_tree=$(git write-tree)
  cp .git/index "$scratch/index-before"
  git diff --cached --binary > "$scratch/staged-before"
  # Deliberately invalid unstaged content proves tests consume the staged snapshot.
  printf '\nthis is invalid unstaged Go\n' >> fixture_test.go
  git diff --binary > "$scratch/worktree-before"
  local status=0 begin=$SECONDS
  bash "$hook" > "$scratch/hook-$count.log" 2>&1 || status=$?
  cmp .git/index "$scratch/index-before"
  [[ $(git write-tree) == "$before_tree" ]]
  git diff --cached --binary > "$scratch/staged-after"
  git diff --binary > "$scratch/worktree-after"
  cmp "$scratch/staged-before" "$scratch/staged-after"
  cmp "$scratch/worktree-before" "$scratch/worktree-after"
  if ! grep -Eq '^ok[[:space:]]+example.invalid/declarations' "$scratch/hook-$count.log"; then
    echo "FAIL $count: real go tests did not pass: $label"
    cat "$scratch/hook-$count.log"
    failed=$((failed + 1))
  elif { [[ $expected == pass ]] && ((status == 0)); } || { [[ $expected == fail ]] && ((status != 0)) && grep -Eq "\\($diagnostic([,)])" "$scratch/hook-$count.log"; }; then
    printf 'PASS %s: %s (%ss)\n' "$count" "$label" "$((SECONDS - begin))"
  else
    printf 'FAIL %s: %s (expected %s, exit %s, %ss)\n' "$count" "$label" "$expected" "$status" "$((SECONDS - begin))"
    cat "$scratch/hook-$count.log"
    failed=$((failed + 1))
  fi
}

fixture gocyclo
complexity 28
full_lint clean
commit_baseline
complexity 31
full_lint gocyclo '^fixture.go:3:1:.*32'
check_hook fail 'body-only complexity 29 to 32' gocyclo

fixture gocyclo
complexity 32
full_lint gocyclo '^fixture.go:3:1:.*33'
commit_baseline
{ printf '// Line shift only.\n\n'; cat fixture.go; } > shifted.go
mv shifted.go fixture.go
printf '\nfunc Unrelated() int { return 42 }\n' >> fixture.go
full_lint gocyclo '^fixture.go:5:1:.*33'
check_hook pass 'unchanged legacy complexity with line shift and unrelated declaration'

fixture gocyclo
complexity 32
commit_baseline
complexity 35
full_lint gocyclo '^fixture.go:3:1:.*36'
check_hook fail 'legacy body-only complexity worsens 33 to 36' gocyclo

fixture gocyclo
complexity 35
commit_baseline
complexity 32
full_lint gocyclo '^fixture.go:3:1:.*33'
check_hook pass 'legacy complexity improves 36 to 33'

fixture paralleltest
printf 'package fixture\n' > fixture.go
full_lint clean
commit_baseline
cat > fixture_test.go <<'GO'
package fixture

import "testing"

func TestSnapshot(t *testing.T) {
}
GO
full_lint paralleltest '^fixture_test.go:5:1:'
check_hook fail 'remove only t.Parallel from unchanged declaration' paralleltest

fixture musttag
cat > fixture.go <<'GO'
package fixture

import "encoding/json"

type Payload struct {
	Name string `json:"name"`
}

func Encode(p Payload) ([]byte, error) {
	return json.Marshal(p)
}
GO
full_lint clean
commit_baseline
cat > fixture.go <<'GO'
package fixture

import "encoding/json"

type Payload struct {
	Name string
}

func Encode(p Payload) ([]byte, error) {
	return json.Marshal(p)
}
GO
full_lint musttag '^fixture.go:10:22:'
check_hook fail 'remove JSON field tag, unchanged struct and marshal call' musttag

fixture gosec
cat > fixture.go <<'GO'
package fixture

import "os"

func ChangeMode(path string) error {
	return os.Chmod(path, 0o600)
}
GO
full_lint clean
commit_baseline
cat > fixture.go <<'GO'
package fixture

import "os"

func ChangeMode(path string) error {
	return os.Chmod(path, 0o777)
}
GO
full_lint gosec '^fixture.go:6:9:.*G302'
check_hook fail 'new insecure chmod permission' gosec

printf '%s cases, %s failures, elapsed %ss (Go 1.27.2, golangci-lint 2.15.0)\n' "$count" "$failed" "$((SECONDS - started))"
((failed == 0))
