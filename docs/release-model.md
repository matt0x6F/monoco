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

**Why one commit can carry all the tags.** Go resolves a version tag → commit, never commit → tag. A single commit can therefore contain B's `go.mod` requiring `C@v0.2.0` while the tag `c/v0.2.0` points at that very commit — there is no ordering constraint to violate. The chicken-and-egg in the sequential flow comes from each tag needing to exist *on the remote* before the next module can build against it; landing every tag in one atomic push removes that gap entirely.

## The direct-affected set

A **direct-affected** module is one whose source is under active local development. monoco identifies it by a workspace-local `replace` directive pointing at it from any sibling module's `go.mod`:

```go
// modules/api/go.mod
replace github.com/org/repo/modules/storage => ../storage
```

That `replace` is the user's declaration of "storage is shipping in this release." During development it lets the workspace compile against uncommitted changes; at release time monoco reads it as intent.

The direct set is the union of two sources: modules detected from `replace` directives, and modules named explicitly via `--bump <module>=<kind>` (any kind but `skip`). The second source exists because a module is releasable regardless of whether anything in the same repo depends on it — a CLI, or a library published only for external consumers, has no in-tree `replace` pointing at it.

All directly-affected modules are then expanded transitively to **cascaded** consumers via the reverse-dep graph. Both sets get the same treatment — default `patch` bump, overridable with `--bump <module>=<kind>` (or `=skip` to drop).

## The bump plan

- Every affected module defaults to `patch`.
- Override per-module: `--bump modules/storage=minor`, `--bump modules/api=major`, `--bump modules/utils=skip`.
- No commit-message inference, no Conventional Commits parsing, no prompting beyond the final `Proceed?`.
- Pre-1.0 versions coerce `Major` → `Minor` (Go's standard semver convention).
- At most one module per release may cross a major-version boundary (the `/vN` path rewrite is heavy; see `internal/propagate/importrewrite/`). Modules opting in must be named with `--allow-major <module>` or listed in `monoco.yaml`'s `allow_major`; the union of both applies.

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

Ordering matters: a cascaded module's *own* `go.mod` and `go.sum` are rewritten by the release, and those files are part of its module zip. Each module is therefore hashed only after its final content is known, walking the plan in topo order (dependencies before consumers), so the `h1:` lines a consumer pins describe the bytes the tag will actually contain. Hashing pre-rewrite state would poison the entry for every mid-chain module — the next module-mode build or `go mod tidy` would fail Go's checksum verification.

Validated by POC-4 — see [poc-findings.md](poc-findings.md).

## Require cycles are staged

Two modules that require each other — directly or transitively — cannot be tagged at the *same commit*. A module's zip includes its own `go.sum`, so A's new zip would have to embed B's new `h1:` hash while B's embeds A's: mutually recursive, no fixed point, for monoco or any other tool. This is a property of Go's checksum model; module-level cycles are legal in Go, and projects that have them (the grpc-go ecosystem, for one) always pin the *previous* version of the other side across the back-edge.

The impossibility is per-commit, not per-push. A release with a cycle becomes an ordered **chain of commits inside one atomic push**:

```
commit 1  B finalized, its go.mod still requiring A@v1.2.3 (the previous tag)
          tag b/v0.4.0 → commit 1
commit 2  A rewritten to require B@v0.4.0 (hashable now — B is final at commit 1)
          tag a/v1.3.0, train tag → commit 2
git push --atomic origin <branch> <all refs>
```

Consumers still see the whole release or none of it, and the history is exactly what a careful release engineer produces by hand.

**Stage planning.** The in-plan require graph is condensed into strongly-connected components, walked in topo order. Singleton components release in the earliest stage their dependencies allow; an acyclic plan is one stage — byte-identical to the unstaged behavior. A multi-member component is peeled one module per stage: a member may peel only if its new content **builds in module mode against the previously tagged content** of the members still unpeeled — which is what external consumers of its new tag will resolve. Cascaded members are tried before direct-affected ones (the actively developed module ships last, seeing the freshest content); `--cut <module>` forces the first peel and fails loudly if that order doesn't compile. Multi-stage commits get a ` (stage s/k)` message suffix; a stage with no on-disk rewrites tags the existing HEAD.

**Pinned verification.** A cycle's back-edge ships requiring the previous tag, so verification materializes that tag's content (`git worktree`) and points the verify `replace` there instead of at the workspace dir — both during cut selection at plan time and in the apply-time verify pass. Note this means `release --dry-run` runs `go build` when (and only when) the plan contains a cycle.

**Release-coupled.** If no member of a component can peel — each side's new code requires the other's new API — no staged order compiles for any tool, and the error says exactly that: the cycle is one module pretending to be two; merge the modules or break the cycle in code. The escape hatches: `--bump <module>=skip` drops one side from the plan, and a cycle whose pinned versions were never tagged cannot be staged at all (there is no previous content to pin).

Major-version bumps are refused for modules inside a cycle: a `/vN` path rewrite cannot apply across a pinned back-edge.

## Atomic publish

```
git push --atomic origin <branch> <per-module tags...> <train tag>
```

`--atomic` has been in git since 2.4 (2015) and is enforced by the git wire protocol, not by GitHub/GitLab/etc. Either every ref lands or none do — including in the pre-receive hook rejection case. No server-side infrastructure assumptions needed.

## Direct push is the contract

Release commits and tags land **only** via monoco's atomic push to a long-lived branch — never through a pull request. Tags pin SHAs and never follow rewrites: a release cut on a PR branch is orphaned the moment the PR is squash- or rebase-merged. The tagged commits stay resolvable by `go get` forever (tag refs keep them alive), but they vanish from the branch's history — `git tag --contains`, bisect, and release auditing all break, and the train tag marks a commit the default branch has never seen. Correctness preserved, hygiene destroyed; monoco refuses to set it up.

Concretely, when a push is intended (`--remote` set), `release` requires the current branch to be the remote's **default branch**, or one matching a `release_branches` glob in `monoco.yaml` (for maintenance branches like `release-1.x`, which are long-lived and direct-pushed too). Feature PRs are unaffected — merge them with squash, rebase, or merge commits as you like; the release happens *after* merge, on the long-lived branch, like the reference CI workflow's `apply` job.

If branch protection forbids all direct pushes, give the release job a bypass (a GitHub App token or PAT with `contents: write`) rather than routing the release through a PR. A strict no-bypass, squash-only policy is the one configuration monoco's model cannot serve.

Before the push, if any step fails (rewrite, verify, commit, tag), the working tree and refs are restored to their pre-run state. If the push itself fails, the local release commit and tags are kept — the work is correct, only publishing failed. After a transient failure, re-push the same refs by hand; if the remote moved ahead, unwind (delete the local tags, reset to the pre-release HEAD) and re-plan — never rebase a release commit, since tags pin SHAs. Step-by-step recovery lives in [operations.md](operations.md#failure-modes-and-recovery).

## TOCTOU protection

Between plan and push, another user might land a commit on the remote base branch. `propagate.Apply` takes a base-SHA lease on the remote ref at the start and fails the push if the remote SHA has moved. The user re-plans against the new base.

## Bootstrap vs steady state

The first propagation of a brand-new monorepo is a special case. Cross-module `require` lines carry placeholder pseudo-versions (`v0.0.0-00010101000000-000000000000`) because no tags exist yet. `modfile.Parse` doesn't care — it treats placeholders as opaque version strings. After the first propagation, placeholders are rewritten to real tagged versions and steady-state behavior takes over.

This bootstrap case is covered by a dedicated integration test; see [test/integration/README.md](../test/integration/README.md).

## Leave-ability

A user who deletes monoco tomorrow keeps a working Go monorepo: standard `go.mod`, standard `go.work`, standard tags. No vestigial files, no broken state, no migration. That's the contract — and every piece of the release pipeline above is designed to preserve it.
