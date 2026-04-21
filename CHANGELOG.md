# Changelog

## Unreleased

### Added

- **Optional `monoco.yaml` manifest** at the repo root for per-module opt-outs and task command overrides. An absent manifest preserves current defaults. ([#1](https://github.com/matt0x6f/monoco/issues/1))
  - `exclude:` — list repo-relative module dirs (same form as `go.work` use entries). Excluded modules are invisible to `affected`, `release`, and task fanout: no tags, no rewrites, not counted as consumers.
  - `tasks.<name>.command:` — override the argv for `test`, `lint`, `build`, or `generate`. Omitted tasks keep their built-in defaults (`go test ./...`, `golangci-lint run`, etc.).
  - Unknown keys and unrecognized task names are rejected at load time so typos aren't silently ignored.
  - `monoco init` writes a commented stub `monoco.yaml` alongside `go.work` if none exists. Re-running `init` never clobbers an existing manifest.

### Technical

- New `internal/config` package. `workspace.Load` now consults the manifest and omits excluded modules from the returned graph; `workspace.LoadWithConfig` exposed for callers that need to inspect the manifest separately.

### Breaking

- **`monoco propagate plan` and `monoco propagate apply` are removed.** Replaced by `monoco release` (see below). Old scripts using `propagate` must migrate to `release --bump <module>=<kind>`.
- **Conventional Commits dependency removed.** Commit-message classification no longer influences bumps. Bump intent is declared at release time, either interactively or via `--bump`. The `internal/convco` package is deleted; `Kind` and `NextVersion` moved to the new `internal/bump` package.
- **`Options.BumpOverrides` renamed to `Options.Bumps`** in `internal/propagate` and is now the sole source of bump intent rather than an override layer.

### Added

- **`monoco release`** — single release command.
  - Detects directly-affected modules from workspace-local `replace` directives.
  - Expands to cascaded consumers via the reverse-dep graph.
  - Every module defaults to a `patch` bump. Override with `--bump <module>=<kind>` (repeatable). `=skip` drops a module from the plan.
  - No prompting: the command reads inputs, prints the plan, asks `Proceed?`, applies. `-y` skips the confirmation.
  - `--dry-run` prints the plan and exits without applying.
- **`go.sum` population at release time.** Each downstream's `go.sum` now gets canonical `h1:` hashes for every freshly-tagged dep, computed in-process via `golang.org/x/mod/zip` + `golang.org/x/mod/sumdb/dirhash` — no network, no proxy, no tag-then-download race. (Validated by POC-4; see [docs/poc-findings.md](docs/poc-findings.md).)
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
- **Workspace graph** built via `modfile.Parse` over each module's `go.mod`, not `go list` (per [POC-1 findings](docs/poc-findings.md)). 50-module chain in ~1ms.
- **Verification** uses `go build -modfile=go.verify.mod` with synthesized `replace` directives (Strategy B from [POC-2](docs/poc-findings.md)).
- **Atomic publishing** via `git push --atomic origin main <tag>...`.
