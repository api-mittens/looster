#!/usr/bin/env bash
# Hermetic hook tests. Commits exist only in disposable fixture repositories.
set -euo pipefail
hook=$(cd "$(dirname "$0")" && pwd)/pre-commit
config=$(dirname "$hook")/../.golangci.yml
real_path=$PATH
if [[ ${1:-} == --real ]]; then
  real_linter=$(command -v golangci-lint)
  real_go=$(command -v go)
  case "$real_linter" in */mise/shims/*) real_linter=$(mise which golangci-lint) ;; esac
  case "$real_go" in */mise/shims/*) real_go=$(mise which go) ;; esac
  real_path="$(dirname "$real_go"):$(dirname "$real_linter"):$PATH"
fi
scratch=$(mktemp -d "${JCODE_SCRATCH_DIR:-${TMPDIR:-/tmp}}/looster-hook-tests.XXXXXXXX")
trap 'status=$?; if (( status != 0 )) && test -f "$scratch/state/output"; then cat "$scratch/state/output" >&2; fi; rm -rf -- "$scratch"' EXIT
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_PREFIX
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null
mkdir "$scratch/bin"
export PATH="$scratch/bin:$PATH"
export HOOK_TEST_STATE="$scratch/state"
mkdir "$HOOK_TEST_STATE"
cat > "$scratch/bin/golangci-lint" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf 'lint\n' >> "$HOOK_TEST_STATE/calls"
[[ "$*" == 'run --fix --new-from-patch='*' ./...' ]]
patch=${3#--new-from-patch=}
test -f "$patch"
cp "$patch" "$HOOK_TEST_STATE/staged.patch"
cp 'a file.go' "$HOOK_TEST_STATE/linted.go"
test ! -e untracked.go
test ! -e .env
if test -f "$HOOK_TEST_STATE/fix"; then
  printf '// fixed\n' >> 'a file.go'
fi
if test -f "$HOOK_TEST_STATE/lint-fail"; then exit 7; fi
MOCK
cat > "$scratch/bin/go" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
[[ "$*" == 'test ./...' ]]
printf 'test\n' >> "$HOOK_TEST_STATE/calls"
cp 'a file.go' "$HOOK_TEST_STATE/tested.go"
if test -f "$HOOK_TEST_STATE/test-fail"; then exit 8; fi
MOCK
chmod +x "$scratch/bin/go" "$scratch/bin/golangci-lint"
count=0
pass() { count=$((count + 1)); printf 'ok %d - %s\n' "$count" "$1"; }
fixture() {
  rm -f "$HOOK_TEST_STATE/"*
  repo="$scratch/repo-$count"
  mkdir "$repo"
  cd "$repo"
  git init -q
  git config user.name 'Hook Test'
  git config user.email hook-test@example.invalid
  printf 'package fixture\n// baseline\n' > 'a file.go'
  printf 'module example.invalid/fixture\n\ngo 1.27.2\n' > go.mod
  git add .
  git -c core.hooksPath=/dev/null commit -qm baseline
}
run_hook() {
  result=0
  bash "$hook" > "$HOOK_TEST_STATE/output" 2>&1 || result=$?
}
assert_preserved() {
  test "$(git write-tree)" = "$before_tree"
  cmp 'a file.go' "$scratch/worktree-before"
}
fixture
run_hook
test "$result" -eq 0 && test ! -f "$HOOK_TEST_STATE/calls"
printf 'docs\n' > readme.md
git add readme.md
run_hook
test "$result" -eq 0 && test ! -f "$HOOK_TEST_STATE/calls"
pass 'empty and docs-only commits skip tools'

fixture
printf '// staged\n' >> 'a file.go'
git add 'a file.go'
cp 'a file.go' "$scratch/staged-before"
printf '// unstaged\n' >> 'a file.go'
printf 'not Go\n' > untracked.go
printf 'fixture-secret\n' > .env
before_tree=$(git write-tree)
cp 'a file.go' "$scratch/worktree-before"
run_hook
test "$result" -eq 0
cmp "$HOOK_TEST_STATE/linted.go" "$scratch/staged-before"
cmp "$HOOK_TEST_STATE/tested.go" "$scratch/staged-before"
assert_preserved
test "$(cat .env)" = fixture-secret
test "$(cat untracked.go)" = 'not Go'
pass 'partial staging and untracked files preserved, staged content tested'

fixture
printf '// staged\n' >> 'a file.go'
git add 'a file.go'
printf '// unstaged\n' >> 'a file.go'
before_tree=$(git write-tree)
cp 'a file.go' "$scratch/worktree-before"
touch "$HOOK_TEST_STATE/fix" "$HOOK_TEST_STATE/lint-fail"
run_hook
test "$result" -ne 0
assert_preserved
printf 'lint\ntest\n' > "$scratch/expected-calls"
cmp "$HOOK_TEST_STATE/calls" "$scratch/expected-calls"
grep -q '// fixed' "$HOOK_TEST_STATE/tested.go"
patches=(.git/looster-lint-fixes.*.patch)
test "${#patches[@]}" -eq 1
grep -Fq '+// fixed' "${patches[0]}"
grep -q 'lint=7, tests=0, fixes=true' "$HOOK_TEST_STATE/output"
pass 'autofix saved as patch, tests after lint failure see fixes, no staging'

fixture
printf '// staged\n' >> 'a file.go'
git add 'a file.go'
touch "$HOOK_TEST_STATE/test-fail"
run_hook
test "$result" -ne 0
grep -q 'lint=0, tests=8, fixes=false' "$HOOK_TEST_STATE/output"
pass 'test failure blocks commit'

fixture
printf '// staged\n' >> 'a file.go'
git add 'a file.go'
touch "$HOOK_TEST_STATE/lint-fail"
run_hook
test "$result" -ne 0
cmp "$HOOK_TEST_STATE/calls" "$scratch/expected-calls"
grep -q 'lint=7, tests=0, fixes=false' "$HOOK_TEST_STATE/output"
pass 'lint failure blocks but never skips tests'

fixture
printf '// staged\n' >> 'a file.go'
git add 'a file.go'
touch "$HOOK_TEST_STATE/fix"
run_hook
test "$result" -ne 0
grep -q 'lint=0, tests=0, fixes=true' "$HOOK_TEST_STATE/output"
pass 'successful autofix still requires review and restaging'

fixture
normal_tree=$(git write-tree)
cp .git/index "$scratch/alternate-index"
export GIT_INDEX_FILE="$scratch/alternate-index"
printf '// alternate staged\n' >> 'a file.go'
git add 'a file.go'
before_tree=$(git write-tree)
cp 'a file.go' "$scratch/worktree-before"
export GIT_DIR="$repo/.git" GIT_WORK_TREE="$repo" GIT_COMMON_DIR="$repo/.git" GIT_PREFIX=''
run_hook
test "$result" -eq 0
assert_preserved
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_PREFIX
test "$(git write-tree)" = "$normal_tree"
grep -q 'alternate staged' "$HOOK_TEST_STATE/linted.go"
pass 'alternate index and inherited Git environment remain isolated'

fixture
# An unborn branch, without deleting or modifying the fixture's existing refs.
git symbolic-ref HEAD refs/heads/unborn
run_hook
test "$result" -eq 0
grep -Fq '+package fixture' "$HOOK_TEST_STATE/staged.patch"
pass 'first commit uses the empty tree as baseline'

fixture
git rm -q 'a file.go'
# Recreate the deleted path only as an untracked file. It must not enter the
# snapshot, so both mocks fail while proving both tools were invoked.
printf 'package fixture\n' > 'a file.go'
# This file is untracked and therefore must not enter the staged snapshot.
run_hook
test "$result" -ne 0
grep -q '^lint$' "$HOOK_TEST_STATE/calls"
grep -q '^test$' "$HOOK_TEST_STATE/calls"
pass 'Go deletion triggers both tools (even when tools fail)'

if [[ ${1:-} == --real ]]; then
  fixture
  cp "$config" .golangci.yml
  cat > legacy.go <<'GO'
package fixture

import "errors"

func Legacy() error { return errors.New("legacy issue outside staged lines") }
GO
  git add .golangci.yml legacy.go
  git -c core.hooksPath=/dev/null commit -qm 'fixture legacy baseline'
  cat > wrap.go <<'GO'
package fixture

import "fmt"

func Wrap(err error) error { return fmt.Errorf("wrap: %v", err) }
GO
  cat > bad.go <<'GO'
package fixture

import "errors"

func Bad() error { return errors.New("new issue") }
GO
  cat > wrap_test.go <<'GO'
package fixture

import (
    "errors"
    "testing"
)

var errSentinel = errors.New("sentinel")

func TestWrap(t *testing.T) {
    t.Parallel()
    if !errors.Is(Wrap(errSentinel), errSentinel) { t.Fatal("wrapping autofix was not applied before testing") }
}
GO
  git add wrap.go bad.go wrap_test.go
  before_tree=$(git write-tree)
  cp 'a file.go' "$scratch/worktree-before"
  cp wrap.go "$scratch/wrap-before"
  PATH="$real_path" run_hook
  test "$result" -ne 0
  assert_preserved
  cmp wrap.go "$scratch/wrap-before"
  grep -q 'err113' "$HOOK_TEST_STATE/output"
  grep -q 'tests=0, fixes=true' "$HOOK_TEST_STATE/output"
  patches=(.git/looster-lint-fixes.*.patch)
  test "${#patches[@]}" -eq 1
  grep -Fq '%w' "${patches[0]}"
  if grep -q 'legacy.go' "${patches[0]}"; then
    echo 'legacy file unexpectedly autofixed' >&2
    exit 1
  fi
  git apply --check "${patches[0]}"
  git apply "${patches[0]}"
  git add wrap.go
  git rm -qf bad.go
  PATH="$real_path" run_hook
  test "$result" -eq 0
  pass 'real pinned linter fixes staged wrapping before tests, rejects new issues, ignores legacy'
fi

printf '%d hook regression cases passed\n' "$count"
