# Changelog

## Unreleased

### Breaking

- **`monoco propagate plan` and `monoco propagate apply` are removed.** Replaced by `monoco release` (see below). Old scripts using `propagate` must migrate to `release --bump <module>=<kind>`.
- **Conventional Commits dependency removed.** Commit-message classification no longer influences bumps. Bump intent is declared at release time, either interactively or via `--bump`. The `internal/convco` package is deleted; `Kind` and `NextVersion` moved to the new `internal/bump` package.
- **`Options.BumpOverrides` renamed to `Options.Bumps`** in `internal/propagate` and is now the sole source of bump intent rather than an override layer.

### Added

- **`monoco release`** — interactive (or `--bump`-driven) release command.
  - Detects directly-affected modules from workspace-local `replace` directives.
  - Expands to cascaded consumers via the reverse-dep graph.
  - Prompts once per direct-affected module for a bump kind; cascades auto-patch.
  - `--bump <module>=<kind>` (repeatable) supplies bumps non-interactively.
  - `--prompt-cascade` surfaces cascaded modules for explicit prompts.
  - `--dry-run` prints the plan and exits.
  - `-y` skips the interactive Proceed? confirmation.
  - Fails closed: any direct-affected module without a bump (and no TTY prompt) is an error, not a silent `None`.
- **`go.sum` population at release time.** Each downstream's `go.sum` now gets canonical `h1:` hashes for every freshly-tagged dep, computed in-process via `golang.org/x/mod/zip` + `golang.org/x/mod/sumdb/dirhash` — no network, no proxy, no tag-then-download race. (Validated by [POC-4](pocs/04-release-gosum/FINDINGS.md).)
- **Workspace-local `replace` directives are stripped** from downstream `go.mod`s as part of the release rewrite. Consumers checking out the released tag now build cleanly.
- **Clean-working-tree preflight** before any release — no uncommitted or untracked changes. Prevents surprise-committing in-flight edits.
- **Auto-rollback on any pre-push failure** — if rewrite, verify, commit, or tag creation fails, the working tree and refs are restored to their pre-run state.

### Technical

- New `internal/bump` package: `Kind` (now includes `Skip`), `Parse`, `NextVersion`. v0 coercion preserved.
- New `internal/propagate/affected_replace.go`: replace-directive affected-module detection.
- New `internal/propagate/gosum.go`: in-process `h1:` hash computation.
- New `internal/release` package: orchestrator + `Prompter` interface + stdio implementation.
- `internal/convco` deleted.

## v0.1.0 — 2026-04-18

First working release. End-to-end propagation flow validated against a fixture monorepo.

### Commands
- `monoco init` / `sync` — generate `go.work` from discovered `go.mod` files.
- `monoco affected --since <ref>` — print affected module set.
- `monoco test | lint | build | generate [--since <ref>|--all]` — parallel task fanout over the affected set (workspace mode preserved).
- `monoco propagate plan --since <ref>` — dry-run a propagation; prints affected modules, bumps, new versions, train tag.
- `monoco propagate apply --since <ref> [--remote <r>] [--slug <s>]` — execute a propagation: rewrite `go.mod`s → release commit → verify in module mode → tag → atomic push.

### Technical approach
- **Workspace graph** built via `modfile.Parse` over each module's `go.mod`, not `go list` (per [POC-1 findings](pocs/FINDINGS.md)). 50-module chain in ~1ms.
- **Verification** uses `go build -modfile=go.verify.mod` with synthesized `replace` directives (Strategy B from [POC-2](pocs/FINDINGS.md)).
- **Atomic publishing** via `git push --atomic origin main <tag>...`.
