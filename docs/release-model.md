# Release model

The semantics of `monoco release` — what a release *is*, how the cascade propagates, how verification catches broken rewrites, and how the atomic push keeps it all honest.

For the code-level organization, see [architecture.md](architecture.md). For the validation work behind each decision, see [poc-findings.md](poc-findings.md).

## The mental model

A release isn't a noun, it's a verb: **propagate a change through the dependency graph atomically**. A single release commit carries every downstream rewrite; a single `git push --atomic` lands every tag or lands none.

Consider `A → B → C` (A depends on B, B depends on C). Shipping a change to C the native Go way takes three sequential releases: tag C, bump B's `go.mod`, tag B, bump A's `go.mod`, tag A. Cross-module refactors become chicken-and-egg — A can't compile against new B until B is tagged, B can't compile against new C until C is tagged.

monoco collapses this into one step:

1. Rewrite B's `go.mod` to pin the new C version.
2. Rewrite A's `go.mod` to pin the new B version.
3. Commit everything.
4. Tag C, B, and A — all pointing at the same commit.
5. Atomic push.

External consumers see honest per-module semver tags, resolvable by `go get` with no monoco knowledge required.

## The direct-affected set

A **direct-affected** module is one whose source is under active local development. monoco identifies it by a workspace-local `replace` directive pointing at it from any sibling module's `go.mod`:

```go
// modules/api/go.mod
replace github.com/org/repo/modules/storage => ../storage
```

That `replace` is the user's declaration of "storage is shipping in this release." During development it lets the workspace compile against uncommitted changes; at release time monoco reads it as intent.

All directly-affected modules are then expanded transitively to **cascaded** consumers via the reverse-dep graph. Both sets get the same treatment — default `patch` bump, overridable with `--bump <module>=<kind>` (or `=skip` to drop).

## The bump plan

- Every affected module defaults to `patch`.
- Override per-module: `--bump modules/storage=minor`, `--bump modules/api=major`, `--bump modules/utils=skip`.
- No commit-message inference, no Conventional Commits parsing, no prompting beyond the final `Proceed?`.
- Pre-1.0 versions coerce `Major` → `Minor` (Go's standard semver convention).
- At most one module per release may cross a major-version boundary (the `/vN` path rewrite is heavy; see `internal/propagate/importrewrite/`). Modules opting in must be listed in `monoco.yaml`'s `AllowMajor`.

## Tag naming

- **Per-module:** `<module-subpath>/v<X>.<Y>.<Z>` — e.g., `modules/storage/v0.9.0`. This is Go's nested-module convention, natively resolvable by `go get`.
- **Release train:** `train/<YYYY-MM-DD>-<slug>` — one per release, pointing at the same release commit as every per-module tag. Useful for human-readable rollback ("which release was this?") without inventing a new concept.

## Verification: `-modfile=<alt>` + replace directives (Strategy B)

The rewrite step can produce an incoherent `go.mod` — e.g., downstream calls a symbol the new upstream doesn't expose. Workspace mode hides this: `go.work`'s `use` entries resolve to on-disk source, so a broken rewrite compiles fine locally and breaks only when external consumers try to `go get` the tagged version.

monoco's verification catches this *before* the push. For each module in the plan:

1. Parse its freshly-rewritten `go.mod`.
2. For every `require` whose target is a workspace module, add a `replace` directive redirecting to the dep's on-disk path.
3. Write the result to a sibling `go.verify.mod` (plus an empty `go.verify.sum`).
4. Run `go build -modfile=go.verify.mod ./...` with `GOWORK=off` and `GOFLAGS=-mod=mod`.
5. Remove both alternate files.

This exercises the rewritten `require` lines against local source in module mode. A broken rewrite surfaces as a standard Go compile error that names the missing symbol or type mismatch — exactly the information the user needs to fix it.

**Environment requirements** (non-obvious, documented for future readers):

- `GOWORK=off` is mandatory. Without it, workspace mode resolves local paths and silently swallows `-modfile`.
- `GOFLAGS=-mod=mod` is mandatory. Without it, `go build` refuses to proceed when `go.verify.sum` has no entries for the replaced deps.
- The alt-modfile path is relative to `cmd.Dir` and must end in `.mod`.
- The real `go.mod` is never touched. The working tree is clean before and after verify (verified by a `git status --porcelain` snapshot comparison).

**Why not Strategy A?** The original plan considered local tags + `GOPROXY=direct`, which relies on Go's direct-mode resolver doing `git ls-remote` against the declared host. It likely works for real hosted monorepos but fails for internal/unhosted module paths, adds network latency, and doesn't exercise compile failures any more precisely than Strategy B. See [POC-2 findings](poc-findings.md#poc-2--gomod-verification-without-remote-tags).

## `go.sum` population

Downstreams' `go.sum` files need canonical `h1:` hashes for the freshly-tagged dep versions — but the tags don't exist on the remote yet, so `go mod download` can't help.

monoco computes the hashes in-process via `golang.org/x/mod/zip` + `golang.org/x/mod/sumdb/dirhash`. No network, no proxy, no tag-then-download race. The hashes are bit-identical to what the Go module proxy would produce after the push, so consumers' `go.sum` verification passes cleanly.

Validated by POC-4 — see [poc-findings.md](poc-findings.md).

## Atomic publish

```
git push --atomic origin <branch> <per-module tags...> <train tag>
```

`--atomic` has been in git since 2.4 (2015) and is enforced by the git wire protocol, not by GitHub/GitLab/etc. Either every ref lands or none do — including in the pre-receive hook rejection case. No server-side infrastructure assumptions needed.

Before the push, if any step fails (rewrite, verify, commit, tag), the working tree and refs are restored to their pre-run state. If the push itself fails, the local release commit and tags are kept; rerun `release` after fixing the push condition (e.g., the remote moved ahead, TOCTOU lease broken).

## TOCTOU protection

Between plan and push, another user might land a commit on the remote base branch. `propagate.Apply` takes a base-SHA lease on the remote ref at the start and fails the push if the remote SHA has moved. The user re-plans against the new base.

## Bootstrap vs steady state

The first propagation of a brand-new monorepo is a special case. Cross-module `require` lines carry placeholder pseudo-versions (`v0.0.0-00010101000000-000000000000`) because no tags exist yet. `modfile.Parse` doesn't care — it treats placeholders as opaque version strings. After the first propagation, placeholders are rewritten to real tagged versions and steady-state behavior takes over.

This bootstrap case is covered by a dedicated integration test; see [test/integration/README.md](../test/integration/README.md).

## Leave-ability

A user who deletes monoco tomorrow keeps a working Go monorepo: standard `go.mod`, standard `go.work`, standard tags. No vestigial files, no broken state, no migration. That's the contract — and every piece of the release pipeline above is designed to preserve it.
