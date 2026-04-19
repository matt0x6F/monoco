# Changelog

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
- **Verification** uses `go build -modfile=go.verify.mod` with synthesized `replace` directives (Strategy B from [POC-2](pocs/FINDINGS.md)). Exercises the rewritten `require` lines against local source without mutating the tracked working tree.
- **Atomic publishing** via `git push --atomic origin main <tag>...`. All-or-nothing even under pre-receive hook rejection (per [POC-3 findings](pocs/FINDINGS.md)).
- All module tags + the train tag point at a single release commit on `main`. The repo transitions atomically from "all old versions consistent" to "all new versions consistent."

### Explicit non-goals for v1
- No `monoco.yaml` manifest (convention-over-configuration).
- No v2+ major-version path rewriting (refused with a clear error).
- No hand-cut-tag forward-propagation flow (manual workaround: `monoco propagate apply --since <hotfix-tag>`).
- No GitHub Action template, no remote task cache, no PR preview markdown.

See [pocs/FINDINGS.md](pocs/FINDINGS.md) for design rationale and [docs/superpowers/plans/2026-04-18-monoco-v1.md](docs/superpowers/plans/2026-04-18-monoco-v1.md) for the full implementation trail.
