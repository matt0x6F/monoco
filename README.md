# monoco

Go-native monorepo tooling: atomic, propagated releases across module boundaries via `go.work` + coordinated git tags.

**The problem it solves.** In a Go monorepo with modules `A → B → C` (A depends on B, B depends on C), shipping a change to C the native way takes three releases: tag C, bump B's `go.mod`, tag B, bump A's `go.mod`, tag A. Cross-module refactors are chicken-and-egg (A won't compile against new B until B is tagged; B won't compile against new C until C is tagged).

**monoco's mental model.** Releasing isn't a noun, it's a verb: *propagate a change through the dependency graph atomically*. While working, you `replace` your in-flight modules locally so the whole workspace compiles. When you're ready, `monoco release` asks which bumps you want, rewrites every downstream `go.mod` + `go.sum`, strips the local `replace` directives, verifies the whole thing compiles in module mode, tags every affected module, and pushes everything atomically. All tags point at a single release commit. External consumers see honest per-module semver.

## Install

```
go install github.com/matt0x6f/monoco/cmd/monoco@latest
```

Requires Go 1.22+.

## Quick start

```bash
# One-time: generate go.work from your existing go.mod files.
monoco init

# During development, pin in-flight modules with a workspace-local replace
# so the whole repo builds against your uncommitted work:
#
#   // modules/api/go.mod
#   replace example.com/mono/storage => ../storage

# When you're ready to ship, run:
monoco release
# Interactive: prompts you for the bump kind of each module that has a
# workspace-local `replace` pointing at it (= under active development).
# Cascaded consumers auto-patch.

# Non-interactive (agents, CI):
monoco release --bump modules/storage=minor --bump modules/api=patch
```

Also useful:
```bash
monoco release --dry-run --bump modules/storage=minor   # preview only
monoco release --prompt-cascade                         # ask about cascades too
monoco affected --since origin/main                     # transitive-affected set
monoco test --since origin/main                         # run tests only where it matters
monoco lint --since origin/main
monoco build --since origin/main
monoco generate --since origin/main
```

## Conventions

- Modules discovered by scanning for `go.mod` files (excluding repo root, `vendor/`, and dotfiles).
- Per-module tags follow Go's nested-module convention: `modules/storage/v0.9.0`.
- Each release gets a train tag pointing at the same release commit: `train/2026-04-18-<slug>`.
- Release commit message: `release: train/<date>-<slug>`.
- **Bump intent is declared at release time** — either interactively or via `--bump <module>=<kind>`. No commit-message inference, no Conventional Commits dependency. The engineer who just finished the work says what the bumps should be.
- A **direct-affected** module is one whose source is under active local development, identified by the presence of a workspace-local `replace` directive pointing at it from any sibling module's `go.mod`.
- A **cascaded** module is a consumer of a direct-affected module; it gets an auto-patch bump unless `--prompt-cascade` is set.
- v2+ major-version boundary crossings are refused (they require `/vN` path rewrites across `module` + `require` + imports; planned for a future release).

## How it works

Every `release` is one atomic operation:

1. Scan each workspace module's `go.mod` for workspace-local `replace` directives. The replaced modules are the **direct-affected** set.
2. Transitively expand to consumers via the reverse-dep graph (the **cascade**).
3. Prompt for direct-affected bumps (or take them from `--bump`). Cascades auto-patch.
4. For each module in the plan, rewrite downstream `go.mod`s to pin the new tag version, drop workspace-local `replace` directives, and populate `go.sum` with the canonical `h1:` hashes (computed in-process — no network, no proxy).
5. Create one release commit containing all the rewrites.
6. Verify in module mode (`-modfile=<alt>` with `replace` directives for workspace siblings) — this catches rewrites that break downstream source, which workspace mode hides.
7. Tag every module in the plan + a train tag, all pointing at the release commit.
8. `git push --atomic origin <branch> <tags...>` — all or nothing.

If anything before the push fails, the working tree and refs are restored to their pre-run state. If the push fails, the local commit and tags are kept; rerun `release` after fixing the push condition.

## Status

Pre-1.0. Design validated by four POCs ([findings](pocs/FINDINGS.md)).

## Not in scope (yet)

- `monoco.yaml` manifest with per-module opt-outs and task command overrides.
- v2+ major-version path rewriting.
- Forward-propagation of orphan tags cut by hand.
- PR creation (intentionally forge-agnostic — wrap with `gh` / `glab` / whatever).
- `monoco doctor`.

## Development

- [Design spec](docs/superpowers/specs/).
- [POC findings](pocs/FINDINGS.md).
- Tests: `go test ./...`. End-to-end CLI: `go test ./cmd/monoco/... -count=1`. Integration against a real GitHub remote: `go test -tags=integration ./test/integration/... -v -count=1`.
