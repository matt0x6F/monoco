# monoco

Go-native monorepo tooling: atomic, propagated releases across module boundaries via `go.work` + coordinated git tags.

**The problem it solves.** In a Go monorepo with modules `A → B → C` (A depends on B, B depends on C), shipping a change to C the native way takes three releases: tag C, bump B's `go.mod`, tag B, bump A's `go.mod`, tag A. Cross-module refactors are chicken-and-egg (A won't compile against new B until B is tagged; B won't compile against new C until C is tagged).

**monoco's mental model.** Releasing isn't a noun, it's a verb: *propagate a change through the dependency graph atomically*. While working, you `replace` your in-flight modules locally so the whole workspace compiles. When you're ready, `monoco release` reads those `replace` directives as your declaration of "these are shipping," rewrites every downstream `go.mod` + `go.sum`, strips the local `replace` directives, verifies the whole thing compiles in module mode, tags every affected module, and pushes everything atomically. All tags point at a single release commit. External consumers see honest per-module semver.

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

# When you're ready to ship, preview:
monoco release --dry-run

# Cut the release (everything defaults to a patch bump):
monoco release -y

# Override the default where a module deserves minor or major:
monoco release -y --bump modules/storage=minor

# Drop a module from this release:
monoco release -y --bump modules/storage=skip
```

Also useful:
```bash
monoco affected --since origin/main     # transitive-affected set
monoco test --since origin/main         # run tests only where it matters
monoco lint --since origin/main
monoco build --since origin/main
monoco generate --since origin/main
```

## Conventions

- Modules discovered by scanning for `go.mod` files (excluding repo root, `vendor/`, and dotfiles).
- Per-module tags follow Go's nested-module convention: `modules/storage/v0.9.0`.
- Each release gets a train tag pointing at the same release commit: `train/2026-04-18-<slug>`.
- Release commit message: `release: train/<date>-<slug>`.
- **Bump kinds default to `patch`** for every module in the plan. Override per-module with `--bump <module>=<minor|major|skip>`. No commit-message inference, no prompting, no Conventional Commits dependency.
- A **direct-affected** module is one whose source is under active local development, identified by the presence of a workspace-local `replace` directive pointing at it from any sibling module's `go.mod`.
- A **cascaded** module is a consumer of a direct-affected module. It gets the same default-patch treatment as directs; override with `--bump` if needed.
- v2+ major-version boundary crossings are refused (they require `/vN` path rewrites across `module` + `require` + imports; planned for a future release).

## How it works

Every `release` is one atomic operation:

1. Scan each workspace module's `go.mod` for workspace-local `replace` directives. The replaced modules are the **direct-affected** set.
2. Transitively expand to consumers via the reverse-dep graph (the **cascade**).
3. Apply bump kinds: every module defaults to `patch`, with `--bump <module>=<kind>` overriding where needed (or `=skip` to drop a module). Print the plan.
4. On confirmation (or `-y`), rewrite every downstream `go.mod` to pin the new tag version, drop workspace-local `replace` directives, and populate `go.sum` with canonical `h1:` hashes (computed in-process — no network, no proxy).
5. Create one release commit containing all the rewrites.
6. Verify in module mode (`-modfile=<alt>` with `replace` directives for workspace siblings) — catches rewrites that break downstream source, which workspace mode hides.
7. Tag every module in the plan + a train tag, all pointing at the release commit.
8. `git push --atomic origin <branch> <tags...>` — all or nothing.

If anything before the push fails, the working tree and refs are restored to their pre-run state. If the push fails, the local commit and tags are kept; rerun `release` after fixing the push condition.

## Status

Pre-1.0. Design validated by four POCs ([findings](pocs/FINDINGS.md)).

## Configuration (`monoco.yaml`, optional)

Drop a `monoco.yaml` at the repo root if you need to deviate from the defaults. `monoco init` writes a commented stub. An absent manifest is equivalent to:

```yaml
version: 1

# Modules excluded from propagation, affected-set, and task fanout.
# Paths match your go.work use entries.
exclude: []

# Per-task argv overrides. Omitted tasks keep their built-in defaults
# (go test ./..., golangci-lint run, go build ./..., go generate ./...).
tasks: {}
```

Example:

```yaml
version: 1
exclude:
  - modules/internal-experimental
  - modules/private-sdk
tasks:
  lint:
    command: ["golangci-lint", "run", "--timeout=5m"]
```

Excluded modules never show up in `monoco affected`, `monoco release`, or task fanout — no tags, no `go.mod` rewrites, not counted as consumers of anything.

## Not in scope (yet)

- v2+ major-version path rewriting.
- Forward-propagation of orphan tags cut by hand.
- PR creation (intentionally forge-agnostic — wrap with `gh` / `glab` / whatever).
- `monoco doctor`.

## Development

- [Design spec](docs/superpowers/specs/).
- [POC findings](pocs/FINDINGS.md).
- Tests: `go test ./...`. End-to-end (local-fixture): `go test ./cmd/monoco/... -count=1`.
- Integration (against the real GitHub test monorepo) runs on every push to `main` via `.github/workflows/integration.yml`. To run locally: `MONOCO_TEST_REPO_TOKEN=<PAT> go test -tags=integration ./test/integration/... -v`. See [test/integration/README.md](test/integration/README.md) for the scenario matrix and troubleshooting.
