# Operations

Setting up monoco, running it day to day, and recovering when something fails.

This is the practical companion to [architecture.md](architecture.md) (how the code is organized) and [release-model.md](release-model.md) (what a release means). If an error message brought you here, jump to [Troubleshooting](#troubleshooting).

## Setup

### Requirements

- Go 1.22+.
- git 2.4+ (`git push --atomic` is the publish primitive).
- For releases: push access to the repo's long-lived branch — the remote's default branch, or one matching a `release_branches` glob in `monoco.yaml`. Releases never go through PRs; see [release-model.md](release-model.md#direct-push-is-the-contract).
- `golangci-lint` on `PATH` only if you use `monoco lint` with its default command.

### First-time setup in a repo

```bash
go install github.com/matt0x6f/monoco/cmd/monoco@latest

cd <repo-root>
monoco init
```

`monoco init` does two things:

1. Writes `go.work` with a `use` entry for every directory containing a `go.mod` (skipping `vendor/`, dotted directories, and the repo root's own `go.mod` if present).
2. Writes a commented `monoco.yaml` stub if none exists. The stub is entirely optional — deleting it changes nothing.

Commit `go.work` (and `go.work.sum`, once workspace commands create it): `release` requires a clean working tree, so untracked workspace files block it.

After moving, adding, or deleting modules, run `monoco sync` (an alias of `init`, idempotent) to refresh `go.work`.

### CI setup

See the [README's CI section](../README.md#using-monoco-in-ci) and the reference workflow at `.github/workflows/monoco-release.yml.example`. The two non-obvious requirements, repeated here because they fail silently or late:

- `actions/checkout` needs `fetch-depth: 0`. `--since` walks real history; a shallow clone silently shrinks the affected set or errors with `unknown revision`.
- The release job's token needs `contents: write` and, under branch protection, a bypass (GitHub App token or PAT) — monoco pushes the release commit and tags directly.

## Day-to-day operation

### During development

Pin in-flight modules with a workspace-local `replace` so the repo builds against your uncommitted work:

```go
// modules/api/go.mod
replace github.com/org/repo/modules/storage => ../storage
```

This is plain Go — the workspace compiles with or without monoco. At release time monoco reads these directives as the declaration of what's shipping.

### Checking what's affected, running tasks

```bash
monoco affected --since origin/main   # transitive-affected module set for the range
monoco test     --since origin/main   # go test ./... in each affected module
monoco lint     --since origin/main   # golangci-lint run (override via monoco.yaml)
monoco build    --since origin/main
monoco generate --since origin/main
```

Task-command flags:

- `--since <ref>` — base ref for the affected computation (branch, remote ref, or SHA). Required unless `--all` is given.
- `--all` — fan out over every workspace module instead of the affected set.
- Arguments after `--` are appended to the task's command, e.g. `monoco test --since origin/main -- -race -count=1` runs `go test ./... -race -count=1` per module.

Tasks run in parallel across modules; output is grouped per module with an `ok`/`FAIL` marker, and the exit code is non-zero if any module failed.

### Cutting a release

```bash
monoco release --dry-run                       # preview the plan, mutate nothing
monoco release -y                              # everything defaults to a patch bump
monoco release -y --bump modules/storage=minor # override a module's bump kind
monoco release -y --bump modules/storage=skip  # drop a module from the plan
monoco release -y --bump modules/cli=minor     # release a module nothing in-repo
                                               # depends on (no replace needed)
```

Release flags:

| Flag | Default | Meaning |
|---|---|---|
| `--bump <module>=<kind>` (repeatable) | every module `patch` | Override the bump kind: `major`, `minor`, `patch`, or `skip`. Naming a module here (any kind but `skip`) also adds it to the release even if no sibling `replace`s it. |
| `--allow-major <module>` (repeatable) | none | Permit the module to cross a `/vN` major boundary. Unioned with `allow_major` in `monoco.yaml`. At most one module per release may cross. |
| `--cut <module>` (repeatable) | auto | In a require cycle, force which member releases first (pinned to its partners' previous tags). |
| `--remote <name>` | `origin` | Push target. `--remote ""` skips the push: the release commit and tags are created locally only. |
| `--slug <slug>` | current branch name | Train-tag slug: `train/<YYYY-MM-DD>-<slug>`. |
| `--dry-run` | off | Print the plan and exit. Offline — no `ls-remote` — except that a plan containing a require cycle runs `go build` to pick the staging order. |
| `-y` | off | Skip the `Proceed? [y/N]` confirmation. |

Module references in `--bump`, `--allow-major`, and `--cut` accept either the module path (the `go.mod` `module` line) or the repo-relative directory (as it appears in `go.work`'s `use` entries) — `modules/storage` works for both forms.

Preconditions checked before anything is mutated:

- The working tree is clean (`git status --porcelain` empty).
- When pushing, the current branch is the remote's default branch or matches a `release_branches` glob.
- When pushing, the remote branch hasn't moved since the plan was computed (re-checked again at push time via a lease).

On success the output reports the release commit SHA, the number of tags created, and whether the push happened. Confirm from anywhere with `git ls-remote --tags origin 'modules/*'`.

## Failure modes and recovery

`release` has a two-phase recovery contract:

**Any failure before the push** — preflight, rewrite, verify, commit, tag — rolls back automatically: locally created tags are deleted and the working tree is reset to the pre-run HEAD. There is nothing to clean up; fix the cause and rerun.

**A failure of the push itself** leaves the local release commit and tags intact (the work is correct; only publishing failed). Re-running `monoco release` at this point reports "nothing to release" — the release commit already stripped the `replace` directives — so recover one of two ways:

- *Transient failure* (network, auth, branch-protection token): re-push the same refs by hand. All of them exist locally:

  ```bash
  git push --atomic origin <branch> <module tags...> train/<date>-<slug>
  ```

- *Remote moved ahead* (non-fast-forward / lease broken): do **not** rebase the release commit — tags pin SHAs and would be orphaned by the rewrite. Unwind and re-plan instead:

  ```bash
  git tag -d <module tags...> train/<date>-<slug>
  git reset --hard <pre-release HEAD>
  git pull origin <branch>
  monoco release ...
  ```

Note on protected branches: monoco pushes with `--force-with-lease` when it holds a base SHA; if the remote's protection rules reject lease pushes outright, it falls back to a plain atomic push (a non-fast-forward is still rejected, so the race protection holds).

## Troubleshooting

Errors below are quoted as monoco prints them (prefixed `monoco:`). Errors from the Go toolchain and git propagate with context rather than being reinterpreted — a compile error during verify is a real compile error in your code.

| Error / symptom | Cause | Fix |
|---|---|---|
| `working tree is not clean: ...` | Uncommitted or untracked files (often `go.work.sum`). | Commit or stash. `go.work` and `go.work.sum` belong in the repo. |
| `no modules have workspace-local replace directives and no --bump overrides; nothing to release.` | monoco found nothing declaring intent to ship. | Add a workspace-local `replace` to a consumer's `go.mod`, or name the module explicitly: `--bump <module>=<kind>`. |
| `refusing to release from branch "x": releases push directly to a long-lived branch ...` | You're on a feature/PR branch. Tags cut there are orphaned by squash/rebase merges. | Release from the default branch (after merging), or add the branch to `release_branches` in `monoco.yaml` if it's a long-lived maintenance branch. |
| `module X would cross major version boundary (v1.9.0 → v2.0.0); pass --allow-major X or set allow_major in monoco.yaml` | Major bumps rewrite the `/vN` path across the module and every consumer — heavy, so it's opt-in. | Add `--allow-major <module>` or the `allow_major` manifest entry. |
| `at most one module per propagation may cross a major boundary; got: ...` | Two or more `/vN` crossings in one plan. | Split into separate releases. |
| `--bump "x=y": module "x" not found in workspace` (also for `--allow-major`, `--cut`, `allow_major`) | Typo, or the module isn't in `go.work`. | Use the module path or the repo-relative directory. `monoco sync` if the module is new. |
| `base moved: origin refs/heads/main was <sha> when plan was computed, now <sha>; re-run 'monoco release' and retry` | Someone pushed to the base branch between plan and apply. Caught before any mutation. | Pull, rerun `monoco release`. |
| `atomic push: ...` | The push itself failed: remote advanced, branch protection, network, auth. Local commit and tags are kept. | See [Failure modes and recovery](#failure-modes-and-recovery). |
| `verify build in <dir>: ... stderr: <compile error>` | A rewrite produced an incoherent module — downstream code doesn't compile against the new upstream in module mode (workspace mode was hiding it). Rolled back automatically. | Fix the source (usually the downstream caller), commit, rerun. The compile error names the symbol. |
| `cannot stage require cycle: X requires Y@<version>, which is not a tag in this repository` | A require cycle can only be staged against previously *tagged* content; the pinned version was never tagged (e.g. bootstrap placeholders). | Tag the partner once by hand, or break the cycle in code. |
| `module X is in a require cycle and would cross a major version boundary` | A `/vN` rewrite can't apply across a pinned cycle back-edge. | Break the cycle first; release the major bump separately. |
| `--cut X: not part of a require cycle in this plan` | `--cut` only chooses an ordering within a cycle. | Drop the flag, or check the plan actually contains the cycle you think it does. |
| Release-coupled cycle error (no member of the cycle compiles against the other's previous tag) | Each side needs the other's *new* API — no staged order works, for monoco or by hand. | The cycle is one module pretending to be two: merge the modules or break the cycle. `--bump <module>=skip` drops one side as a stopgap. |
| `affected`/`test` with `--since` errors with `unknown revision`, or the affected set looks too small | Shallow clone, or the base ref isn't fetched. | `git fetch origin main` locally; `fetch-depth: 0` in CI. |
| `monoco lint` fails with `executable file not found` | Default lint command is `golangci-lint run` and the binary isn't installed. | Install golangci-lint, or override `tasks.lint.command` in `monoco.yaml`. |
| Excluded module still being tagged / still in the affected set | `exclude` entries are repo-relative directories matching `go.work` `use` entries, not module paths. | Use the directory form (`modules/foo`), then re-check with `monoco affected`. |
| `monoco.yaml: ... field <x> not found` | Unknown keys are rejected (typo guard), as are unknown task names. | Valid keys: `version`, `exclude`, `tasks` (test/lint/build/generate), `allow_major`, `release_branches`. |

## Uninstalling

Delete the binary and `monoco.yaml`. Everything else monoco touched is standard Go and git: `go.work`, `go.mod`, `go.sum`, semver tags. Nothing to migrate, nothing breaks — that's the [leave-ability contract](release-model.md#leave-ability).
