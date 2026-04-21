# monoco

Go-native monorepo tooling: atomic, propagated releases across module boundaries via `go.work` + coordinated git tags.

**The problem it solves.** In a Go monorepo with modules `A → B → C` (A depends on B, B depends on C), shipping a change to C the native way takes three releases: tag C, bump B's `go.mod`, tag B, bump A's `go.mod`, tag A. Cross-module refactors are chicken-and-egg (A won't compile against new B until B is tagged; B won't compile against new C until C is tagged).

**monoco's mental model.** Releasing isn't a noun, it's a verb: *propagate a change through the dependency graph atomically*. While working, you `replace` your in-flight modules locally so the whole workspace compiles. When you're ready, `monoco release` reads those `replace` directives as your declaration of "these are shipping," rewrites every downstream `go.mod` + `go.sum`, strips the local `replace` directives, verifies the whole thing compiles in module mode, tags every affected module, and pushes everything atomically. All tags point at a single release commit. External consumers see honest per-module semver.

**Where it fits.** Go's toolchain builds, tests, and versions modules. `go.work` handles local cross-module development. The gap is at release time, when a cross-module change needs to become a coherent set of tags in one step. That's what monoco does, and that's all it does. Delete it tomorrow and you still have a working Go monorepo: standard `go.mod`, standard `go.work`, standard tags. See [Why not X?](#why-not-x) for how that stacks up.

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

## Using monoco in CI

monoco is a CLI, so CI calls it like any other Go binary. A reference
workflow lives at [`.github/workflows/monoco-release.yml.example`](.github/workflows/monoco-release.yml.example)
— copy it into your repo (drop the `.example`) and tweak.

The workflow splits into two jobs that reflect the two places monoco runs:

- **`plan` on `pull_request`** — `go install`s monoco, runs
  `monoco test --since "origin/$BASE_REF"` to fail fast on affected
  tests, then runs `monoco release --dry-run` and posts the output as a
  sticky PR comment (so reviewers see the propagation plan without
  running it locally).
- **`apply` on `push` to `main`** — re-installs monoco and runs
  `monoco release -y --remote origin`, which cuts the release commit,
  tags every affected module, and atomically pushes.

Gotchas worth flagging when you copy it:

- `actions/checkout@v4` must use `fetch-depth: 0`. monoco's `--since`
  needs real history; the default shallow clone silently drops modules
  from the affected set.
- `permissions:` differ per job. `plan` needs `contents: read` plus
  `pull-requests: write` (for the comment). `apply` needs `contents: write`
  (for the release commit and tags).
- The default `GITHUB_TOKEN` can push to `main` and create tags in the
  same repo. If branch protection blocks bot pushes, swap in a PAT or a
  GitHub App token with `contents: write` on `actions/checkout` and
  update the `apply` job accordingly.
- Major-version bumps across the `/vN` boundary are refused today
  (see [Conventions](#conventions)); the workflow won't rescue you from
  that — `monoco release --dry-run` will just fail the `plan` job.

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

## Why not X?

Every option below fails a Go monorepo in one of two ways: it doesn't know what a Go module is, or it knows too much and takes over. Here's where each one lands.

**Your monorepo is probably not the only monorepo.** Other repos, inside your org or outside it, import your modules with plain `go get github.com/you/repo/modules/storage@v1.4.0`. That works because monoco produces the tags Go already understands: per-module semver at the module's path, resolvable by the proxy, verifiable against `go.sum`. Anything that breaks that contract — a custom resolver, a Bazel artifact that isn't `go get`-able, a versioning scheme the proxy doesn't index — pushes the cost onto every downstream consumer. Keeping Go's module semantics intact is what lets a Go monorepo coexist with the rest of your ecosystem.

### Why not a single root `go.mod`?

Often the right call. If one module fits, stop reading: one version, one tag, one `go.sum`.

It stops fitting when parts of the repo have different audiences — a library you publish, a service you deploy, a CLI with its own cadence. One module forces them onto one version number and one release clock, and external consumers either pull the whole repo as a dependency or nothing.

monoco picks up where you've already decided multiple modules is correct.

### Why not `go.work` alone?

`go.work` is the other half of this story. Use it either way. It makes the workspace compile against your uncommitted changes.

What it doesn't do is release. Publishing a cross-module change drops you back into the N-tag chicken-and-egg: B can't be tagged until it references a tagged C, A can't be tagged until it references a tagged B. Most teams cover this with a release script that accumulates edge cases over time. monoco is that script, with atomic pushes and in-process `h1:` hashing so a release either lands whole or not at all.

### Why not Bazel (or Pants, or Please)?

Fit-for-purpose in large polyglot repos with heavy caching and remote execution. If you're building Go, Java, Python, and TypeScript against a build farm, that's the trade.

For a Go-only monorepo the shape is different. `BUILD.bazel` files sit alongside every `go.mod`, usually generated by `gazelle`, and the Go toolchain stops being the source of truth for what builds. The external cost is the one above: downstream repos that want to `go get` one of your modules are now negotiating with your build system instead of Go's proxy.

monoco doesn't go there. No toolchain replacement, no parallel graph, no ownership of your build.

### Why not Nx, Turborepo, or Lerna?

Built for JS ecosystems and good at what they do there. On a Go repo they work from the outside: they orchestrate tasks and cache outputs, but they don't read `go.mod`, don't compute `go.sum` hashes, and don't know what a nested-module tag means to `go get`. You end up redescribing the module graph in their config instead of letting `go.mod` be the source.

Mostly-JS repo with a Go service on the side, reach for these. The other way around, the seams show.

### Why not a hand-rolled release script?

This is what monoco actually competes with. For two or three modules, a shell script is fine. It gets uncomfortable once you need transitive cascade detection, deterministic `go.sum` rewrites without a proxy, atomic multi-tag pushes with rollback, and a dry-run that reflects what will happen. Those are the pieces worth extracting.

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
