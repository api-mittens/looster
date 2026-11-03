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
mise run hooks:test                    # mock regressions and audit helper unit tests
mise run hooks:test-real               # real autofix and declaration-boundary regressions
```

The hook uses the staged snapshot against HEAD, not unstaged working files or a
moving branch merge base. On an unborn branch the baseline is empty. It applies
two complementary incremental policies:

- `gocyclo`, `paralleltest`, `musttag`, and `gosec` are audited across the complete
  baseline and candidate trees under the same candidate configuration. A small
  standard-library-only Go helper compares diagnostics by linter, file, named
  declaration (including method receiver), and normalized message, not line
  number. It preserves diagnostic multiplicity. Newly introduced findings block
  even when reported at an unchanged declaration. Moving an unchanged legacy
  finding down the file does not make it new.
- Complexity findings additionally compare their numeric score. Crossing the
  configured threshold or worsening an already-over-threshold function blocks.
  Unchanged or improved legacy complexity is allowed, even when its declaration
  line is edited. The configured threshold remains 30.
- Other linters retain changed-line filtering and autofixes. Existing issues
  elsewhere in a touched file do not suddenly block unrelated work. The four
  declaration-audited linters are excluded from this line-filtered pass so an
  unchanged legacy diagnostic is not accidentally rejected twice.

The manual `lint` task remains a quick changed-line preview against HEAD, not a
substitute for the stronger hook or checking the exact staged commit. Full audits
at introduction reported 402 legacy findings. This task does not disable or
mass-fix them. Typechecking and `go test ./...` still check all packages. For
linters outside the declaration-aware set, changed-line filtering can still miss
diagnostics reported on unchanged declarations. Use the full audit for
cross-cutting changes or legacy cleanup.

## Safe staged snapshots and autofixes

For commits touching Go files (including deletions and renames), module/workspace
files, lint configuration, `mise.toml`, or the hook itself, the hook:

1. Copies the index into a temporary directory with `git checkout-index`. Unstaged
   edits, untracked files, ignored local configuration, and secrets are not copied.
   Git's alternate index for path-limited commits is respected.
2. Resolves mise shim executables in the original trusted project directory, so
   the snapshot does not require mise trust or an untracked dotenv file.
3. Runs the changed-line autofix pass on that copy, excluding the four
   declaration-audited linters.
4. Audits `gocyclo`, `paralleltest`, `musttag`, and `gosec` in the HEAD baseline
   and fixed candidate with the same configuration, then compares their JSON
   diagnostics. Audit errors fail closed rather than silently skipping checks.
5. Runs **`go test ./...` after fixes and audits**, even if lint reports issues
   or errors.
6. If tracked snapshot files changed, saves a binary-capable patch to
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

The declaration-aware hook was measured twice against the real staged project
using populated tool/module caches: **30.083 seconds** and **33.115 seconds**.
Both runs reported zero new issues, passed all default Go tests, and preserved
the original index. Web tests accounted for 21.765 and 25.499 seconds respectively.
The extra baseline/candidate audits intentionally trade some runtime for catching
new findings at unchanged declarations. The previous changed-line-only hook's
17.8-second run did not provide that safeguard and is not a measurement of the
corrected implementation.

The safeguard/autofix regression suite passed all 12 cases, including failed audit
and comparison paths. Seven additional real-tool declaration scenarios passed in
23 seconds in total. These are measured local runs, not cold-cache guarantees.
Fresh downloads, hardware, and enabled integration tests affect runtime.
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
  The real-tool suites verify actual autofixes before tests, err113 rejection,
  complexity threshold crossings and worsening legacy complexity, body-only
  paralleltest violations, musttag/gosec findings, unchanged legacy diagnostics,
  manual patch application, and staged-file preservation.
- The audit helper is a separate standard-library-only Go module so its bootstrap
  implementation is not part of the application's lint inputs. The `hooks:test`
  mise task and CI explicitly run its unit tests. Source files renamed to a new
  path or declarations renamed to a new identity can make old findings count as
  new. Replacing one finding with an indistinguishable finding in the same
  declaration and message is not distinguishable by normalized comparison.
