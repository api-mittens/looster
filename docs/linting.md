# Go linting and pre-commit checks

## Install

The repository pins Go **1.27.2** and golangci-lint **2.15.0** in `mise.toml`.
The installed Linux linter reports that it was built with Go 1.27.0, the same
Go language release as this project's 1.27.2 patch version. Configuration uses
the golangci-lint v2 schema. Verify installations with:

```sh
mise install
go version
golangci-lint version
golangci-lint config verify
```

There were no active repository hooks or configured `core.hooksPath` when this
setup was introduced. Review any current configuration before replacing it:

```sh
git config --show-origin --get core.hooksPath
mise run hooks:install
# Equivalent installation, no file copies required:
git config --local core.hooksPath scripts
```

A missing hooksPath query normally returns exit status 1. The hook is executable
and uses Bash, Git, and standard Unix utilities. Installation is opt-in and only
changes local Git configuration. If you already use another hook manager, chain
`scripts/pre-commit` from it instead of replacing its hooksPath. To uninstall,
restore your previous hooksPath or, if none existed, run
`git config --local --unset core.hooksPath`.

## Policy

The deliberately limited suite enables `errcheck`, `govet`, `staticcheck`,
`unused`, `gocyclo`, `paralleltest`, `musttag`, `gosec`, `modernize`, `err113`
(not `err13`), and `errorlint`. The complexity threshold is 30. Generated code
is excluded using strict generated-file markers. Existing standard false-positive
presets apply, rather than blanket exclusions for tests or application packages.

```sh
mise run lint                         # lint changes relative to HEAD, no fixes
# For branch/CI changes relative to the common ancestor:
golangci-lint run --new-from-merge-base=master ./...
golangci-lint run ./...                # deliberate full legacy audit
mise run test
mise run hooks:test                    # fast isolated mock regressions
bash scripts/pre-commit_test.sh --real # also use installed Go and linter
```

The hook uses an explicit **staged diff against HEAD**, not the worktree or a
moving branch merge base. On an unborn branch the baseline is the empty tree.
Only diagnostics located on added/changed lines are enforced. This is the
incremental adoption boundary: existing issues elsewhere in a touched file do
not suddenly block unrelated work. `--whole-files` is deliberately not used.
The manual `lint` task compares the working tree against HEAD and is a convenience,
not a substitute for checking the exact staged commit.

A full audit at introduction reported **402 legacy findings**. These are not
silently disabled or mass-fixed by this task. Typechecking and `go test ./...`
still check all packages. Line-based filtering can miss a diagnostic reported
on an unchanged declaration even when a change elsewhere caused it. Use the
full audit when reviewing cross-cutting changes or scheduling legacy cleanup.

## Safe staged snapshots and autofixes

For commits touching Go files (including deletions and renames), module/workspace
files, lint configuration, `mise.toml`, or the hook itself, the hook:

1. Copies the index into a temporary directory with `git checkout-index`. Unstaged
   edits, untracked files, ignored local configuration, and secrets are not copied.
   Git's alternate index for path-limited commits is respected.
2. Resolves mise shim executables in the original trusted project directory, so
   the snapshot does not require mise trust or an untracked dotenv file.
3. Runs `golangci-lint run --fix --new-from-patch=... ./...` on that copy.
4. Runs **`go test ./...` after fixes**, even if lint reports issues or errors.
5. If tracked snapshot files changed, saves a binary-capable patch to
   `looster-lint-fixes.<unique>.patch` inside the original Git directory and blocks
   the commit. The exact absolute patch path is printed.

The hook never stashes, overwrites working files, or stages changes in the real
index. Partially staged files remain partially staged. Fixes always require
review, even when the linter and tests succeed:

```sh
git apply --check /absolute/path/printed/by/the/hook.patch
git apply /absolute/path/printed/by/the/hook.patch
git diff
# Select only the intended hunks, especially for partially staged files:
git add -p
# Retry your commit after reviewing the staged diff.
```

If a patch conflicts with unstaged changes, do not force-apply it. Review it and
apply the desired edits manually. Remove the saved patch when no longer needed.
An ordinary docs-only or empty commit skips lint and tests. Failure of either
tool, or the presence of fixes needing review, blocks a relevant commit.

## Local verification and runtime

With golangci-lint 2.15.0 on the development machine, configuration validation
passed and the real-tool hook regression suite passed all 10 cases in about
5 seconds. A staged project snapshot ran lint with autofixes (zero new findings)
and all default Go tests in **17.8 seconds**, without changing the original index.
Most of that run was the web package tests (14.8 seconds), not lint output.
These are measured local runs with populated tool/module caches, not a cold-cache
guarantee. Fresh downloads, hardware, and enabled integration tests affect runtime.
Docs-only and empty commits skip both tools.

## Limitations

- Tests run on staged content, not your unstaged working state. They cannot rely
  on untracked fixtures, an untracked `.env`, local generated files, or ignored
  build outputs. Provide required runtime services/configuration explicitly.
- The snapshot is a temporary Git repository with no project history or commits.
  Tests that need history, submodules, Git LFS/filter materialization, or a
  particular checkout path require separate integration validation. This project
  currently has no staged submodules or custom checkout filters.
- Lint uses read-only module mode. Tests can still use the normal shared Go build
  and module caches and may need network access or external dependencies. The
  snapshot is protection against accidental staging, not a security sandbox.
- Any tracked-file changes made by tests are also saved in the review patch.
  Newly created untracked files inside the snapshot are discarded during cleanup.
- Missing tool/setup failures before lint starts abort immediately. Once lint
  starts, its nonzero status does not prevent running tests. Interrupts stop the
  hook and remove its temporary copy.
- Hook regressions create commits only inside disposable fixture repositories.
  `--real` additionally verifies actual errorlint autofixes before tests, new
  err113 failures, manual patch application, and preservation of legacy issues.
