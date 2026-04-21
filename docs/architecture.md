# Architecture

How monoco is organized internally and how data flows through a release.

For the "why" behind the key design decisions, see [poc-findings.md](poc-findings.md). For the semantics of what a release *does*, see [release-model.md](release-model.md).

## Guiding principles

Monoco aligns Go's existing build and release tooling for monorepo use. Two principles shape every package boundary:

- **Derive, don't declare.** Every fact about the module graph comes from `go.mod`, `go.work`, or git. There is no parallel manifest of modules, versions, or dependencies.
- **No network during propagate or release.** `go.sum` hashes are computed in-process. Verification uses local source. The only network touches are the initial `git fetch` the user invokes and the final `git push`.

The corollary: deleting monoco leaves a working Go monorepo — standard `go.mod`, standard `go.work`, standard tags.

## Package map

All internal packages live under `internal/`. Each has one job.

### `internal/workspace`

Parses `go.work`, enumerates workspace modules, and builds the workspace-internal dependency graph. The graph oracle is `modfile.Parse` over each module's `go.mod` — not `go list` (see [POC-1 findings](poc-findings.md#poc-1--reverse-dep-graph-from-gowork--gomod-files)).

**API:** `Load`, `LoadWithConfig`, `Workspace.Modules`, `Workspace.Consumers`.

Only edges whose target is itself a workspace module (has a `use` entry in `go.work`) are kept. External deps are irrelevant to monoco's job.

### `internal/gitgraph`

Read-only git operations: commit range enumeration, touched-file listing, latest-tag lookup per module prefix. Shells out to `git` directly.

**API:** `CommitsInRange`, `TouchedFiles`, `LatestTagForModule`.

### `internal/affected`

Given a set of directly-touched modules, computes the transitive reverse-dep closure over the workspace graph.

**API:** `Compute` (closure over module paths), `FromTouchedFiles` (maps repo-relative paths → affected module paths).

### `internal/bump`

Owns semver bump kinds: `Major`, `Minor`, `Patch`, `Skip`. Pre-1.0 versions treat `Major` as `Minor` (standard Go semver convention).

**API:** `Kind`, `Parse`, `NextVersion`.

### `internal/config`

Loads the optional `monoco.yaml`. Absence is equivalent to zero-config defaults — convention beats configuration.

**API:** `Config` with `Exclude`, `Tasks`, `AllowMajor` fields.

### `internal/propagate`

The heavy lifter. Two phases:

- **`plan.go`** — from a set of direct-affected modules, expand to consumers (auto-patched), enforce the at-most-one-major-boundary rule, and produce an ordered list of `Entry` values with rewrite metadata (new versions, tag names, go.mod rewrites).
- **`apply.go`** — execute the plan: rewrite `go.mod` / `go.sum`, create the release commit, tag, and push atomically. TOCTOU-protected via base-SHA leases on the remote ref.
- **`gosum.go`** — in-process canonical `h1:` hash computation via `golang.org/x/mod/zip` + `sumdb/dirhash`. No network (see [POC-4 findings](poc-findings.md)).
- **`importrewrite/`** — v2+ path rewriting across `module`, `require`, and imports for major-version boundary crossings.

**API:** `Plan`, `Entry`, `Options`, `ApplyResult`, `NewPlanForModules`, `Apply`, `ComputeRewrites`, `CascadeExpansion`.

### `internal/release`

The `monoco release` orchestrator. Detects direct-affected modules from workspace-local `replace` directives, applies bump kinds (default `patch`, overridable via `--bump`), and delegates the rewrite/commit/tag/push to `propagate`.

**API:** `Options`, `Plan`, `Apply`, `CurrentVersions`.

### `internal/tasks`

Parallel command fanout (`monoco test|lint|build|generate`) over the affected set. Preserves workspace mode — unlike `propagate`'s verification step, task fanout *wants* cross-module deps resolved to on-disk paths.

**API:** `Result`, `Run`, `AnyFailed`.

### `internal/fixture`

Local git-repo fixtures for end-to-end tests. Not used at runtime.

## CLI layer

`cmd/monoco/main.go` wires subcommands and does nothing else — no direct `git` or `go` shelling. All external-process work lives in helper packages (`gitgraph`, `tasks`, `propagate`).

Subcommands:

| Command | Package | Purpose |
|---|---|---|
| `init`, `sync` | `workspace` | Generate/refresh `go.work` from discovered `go.mod` files. |
| `affected` | `affected` + `gitgraph` | Print the transitive-affected module set. |
| `test`, `lint`, `build`, `generate` | `tasks` | Parallel task fanout. |
| `release` | `release` → `propagate` | Plan and execute an atomic release. |

## Data flow: a release

```
CLI args (cmd/monoco)
  ↓
workspace.Load         — parse go.work + all go.mod files
  ↓
release.Options        — detect replace directives → direct-affected set
  ↓
propagate.CascadeExpansion — reverse-dep closure → full affected set
  ↓
bump.NextVersion       — apply bump kinds per module
  ↓
propagate.ComputeRewrites  — new go.mod/go.sum content + tag names
  ↓
dry-run stops here; --yes continues ↓
  ↓
propagate.Apply:
  1. Write rewrites to disk
  2. Verify in module mode (-modfile=go.verify.mod, see release-model.md)
  3. git commit -m "release: train/<date>-<slug>"
  4. git tag <per-module tags> + train tag
  5. git push --atomic origin <branch> <tags...>
  ↓
On any pre-push failure: restore working tree + refs to pre-run state.
```

## Invariants agents and contributors should preserve

- **`modfile.Parse` is the workspace-graph oracle.** Don't reach for `go list` — it introduces subprocess fanout and network-dependent failure modes for information we don't need.
- **No network during propagate or release.** `go.sum` hashes are computed locally; verification runs against on-disk source.
- **Workspace-local `replace` directives are the source of truth for "what's shipping."** They declare the direct-affected set.
- **Tags are standard.** `<module-path>/vX.Y.Z` for modules, `train/<date>-<slug>` for release trains. No custom resolver, no alternate version format.
- **Atomic all-or-nothing pushes.** `git push --atomic` is the single publish operation; no server-side infrastructure assumptions.
