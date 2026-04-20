# monoco

Go-native monorepo tooling: atomic propagation of changes across module boundaries via `go.work` + coordinated git tags.

**The problem it solves.** In a Go monorepo with modules `A → B → C` (A depends on B, B depends on C), shipping a change to C the native way takes three releases: tag C, bump B's `go.mod`, tag B, bump A's `go.mod`, tag A. Cross-module refactors are chicken-and-egg (A won't compile against new B until B is tagged; B won't compile against new C until C is tagged).

**monoco's mental model.** Releasing isn't a noun, it's a verb: *propagate a change through the dependency graph atomically*. Change C, and `monoco propagate apply` rewrites B's `go.mod`, rewrites A's `go.mod`, verifies the whole thing compiles in module mode, tags every affected module, and pushes everything atomically. All tags point at a single release commit. External consumers see honest per-module semver. No cascade ceremony.

## Install

```
go install github.com/matt0x6f/monoco/cmd/monoco@latest
```

Requires Go 1.22+.

## Quick start

```bash
# One-time: generate go.work from your existing go.mod files.
monoco init

# Your team works on modules. Say you change `storage/` with a feat commit.
# Cross-module refactors are fine in one PR — workspace mode handles it.

# See what would happen:
monoco propagate plan --since origin/main

# Ship it:
monoco propagate apply --since origin/main
```

Also useful:
```bash
monoco affected --since origin/main          # Which modules changed (transitively)?
monoco test --since origin/main              # Only test what changed.
monoco lint --since origin/main              # Same, for golangci-lint.
monoco build --since origin/main
monoco generate --since origin/main
```

## Conventions (v1 is convention-over-configuration)

- Modules discovered by scanning for `go.mod` files (excluding repo root, `vendor/`, and dotfiles).
- Per-module tags follow Go's nested-module convention: `modules/storage/v0.9.0`.
- Each propagation gets a train tag pointing at the same release commit: `train/2026-04-18-<slug>`.
- Release commit message: `release: train/<date>-<slug>`.
- Bump kinds derived from [Conventional Commits](https://www.conventionalcommits.org/): `feat:` → minor, `fix:`/`perf:`/`refactor:` → patch, `!:` or `BREAKING CHANGE` → major.
- v2+ major-version boundary crossings are refused in v1 (they require `/vN` path rewrites across `module` + `require` + imports; planned for v1.1).

## How it works

Every `apply` is one atomic operation:

1. Compute the affected module set from the commit range, transitively via reverse-deps.
2. Classify each module's bump from Conventional Commits in that range.
3. Rewrite each module's `go.mod` to reference the new versions of other modules in the plan.
4. Create one release commit containing all the rewrites.
5. Verify in module mode (`-modfile=<alt>` with `replace` directives) — this catches rewrites that break downstream source, which workspace mode hides.
6. Tag every affected module + a train tag, all pointing at the release commit.
7. `git push --atomic origin main <tags...>` — all or nothing.

If verification fails, the release commit is rolled back and no tags are created. If the push fails, local tags are kept and `apply` is resumable.

## Status

Pre-1.0. v0.1.0 is the first working end-to-end release. Design validated by three POCs ([findings](pocs/FINDINGS.md)).

## Not in v1 (tracked for later)

- `monoco.yaml` manifest with per-module opt-outs and task command overrides.
- v2+ major-version path rewriting.
- Hand-cut-tag-to-forward-propagation PR flow.
- GitHub Action template.
- `monoco doctor`, `propagate preview --pr <n>` (PR comment markdown).

## Development

- [Design spec](docs/superpowers/specs/) (via brainstorming session — see git history).
- [POC findings](pocs/FINDINGS.md).
- [v1 implementation plan](docs/superpowers/plans/2026-04-18-monoco-v1.md).
- Tests: `go test ./...`. End-to-end (local-fixture): `go test ./cmd/monoco/... -count=1`.
- Integration (against the real GitHub test monorepo) runs on every push to `main` via `.github/workflows/integration.yml`. To run locally: `MONOCO_TEST_REPO_TOKEN=<PAT> go test -tags=integration ./test/integration/... -v`. See [test/integration/README.md](test/integration/README.md) for the scenario matrix and troubleshooting.
