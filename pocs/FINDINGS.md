# monoco POC Findings

**Date:** 2026-04-18
**Environment:** go1.25.0 darwin/arm64, git 2.50.1 (Apple Git-155)

All three technical bets from the design spec cleared. Two material findings
change the v1 implementation from what the design originally contemplated.

## Summary

| POC | Bet | Result | Implication for v1 |
|-----|-----|--------|--------------------|
| 1 | Reverse-dep graph from `go list -m -json all` + `go.work` | **Cleared (with pivot)** | Use `modfile.Parse` as the primary data source, not `go list`. |
| 2 | go.mod consistency verification without remote tags | **Cleared (with pivot)** | Use `-modfile=<alt>` + replace directives (Strategy B), not local tags + `GOPROXY=off` (Strategy A). |
| 3 | `git push --atomic` is all-or-nothing across branch + N tags | **Cleared** | Use as-spec'd; no mitigations needed. |

---

## POC-1 — reverse-dep graph from `go.work` + go.mod files

**Bet:** we can compute transitive reverse-dep affected sets across workspace
modules without parsing Go source, in a sub-second walk.

**Result:** viable.

### Finding: `go list -m -json all` is the wrong primary data source

The plan proposed running `go list -m -json all` (with `GOWORK=off`) in each
workspace module to enumerate its dependencies. This does not work against
workspace modules whose cross-module `require` lines use placeholder
pseudo-versions like `v0.0.0-00010101000000-000000000000` (the canonical
placeholder for "not yet released," which is the normal state for a monorepo
module that has not yet been tagged). `go list` refuses to resolve these —
it tries to fetch `example.com/mono/storage@v0.0.0-00010101000000-000000000000`
and fails with `unrecognized import path ... 404 Not Found` before emitting any
JSON.

This is not a fixture artifact. A real monorepo using the "everything is
unreleased until the first propagation" convention would hit the same issue.

### The pivot: `modfile.Parse`

Reading each module's `go.mod` directly via `golang.org/x/mod/modfile.Parse`
yields exactly the edges we need:
- `modfile.File.Module.Mod.Path` → this module's path.
- `modfile.File.Require[i].Mod.Path` → declared dependencies.

We keep only edges whose target is itself a workspace module (i.e., has a
`use` entry in `go.work`), which gives us the reverse-dep graph.

This satisfies the "no source parsing" constraint — `go.mod` is structured
metadata, not Go source — and is strictly faster than subprocess fanout to
`go list`.

### Numbers

- **3-module fixture (storage, api→storage, auth):** BuildGraph = **150µs**, well under the <1s bound.
- **50-module chain (worst case for reverse-dep walk):** BuildGraph = **1.03ms**, Affected walk = **4.6µs**. Two orders of magnitude under the 10s kill bound.

### Recommendations for v1

- Use `modfile.Parse` as the primary data source for the module graph in `internal/workspace/graph.go`.
- Preserve a `BuildGraphWithGoList` escape hatch that calls `go list` for monorepos where every cross-module require is resolvable (real tags, no placeholders) and where richer transitive-of-transitive info is genuinely useful. The POC's `main.go` keeps this as an alternate mode behind a flag.
- Do **not** bother parallelizing the go.mod read fanout in v1. 1ms for 50 modules is already well under any reasonable bound. Revisit only if we see 1000+ module repos.

---

## POC-2 — go.mod verification without remote tags

**Bet:** we can verify a rewritten `go.mod` is self-consistent (build-wise)
before any tag exists on any remote, without mutating the real go.mod.

**Result:** viable.

### Finding: `GOPROXY=off` + local tags (Strategy A) is not viable for module paths that don't resolve to real VCS

The plan's primary approach — tag the new version locally, run
`go build` with `GOWORK=off GOPROXY=off`, expect Go to resolve via direct VCS
— does not work for the fixture (and won't work for any monorepo that uses
module paths like `example.com/mono/<name>` or internal hosts that aren't
publicly resolvable). `GOPROXY=off` tells `go` to use only the module cache;
the cache is populated by fetching, which requires the network or a proxy;
local git tags in the *monorepo's* repo don't satisfy a fetch for
`example.com/mono/storage`, because Go looks up *that* URL, not the
monorepo's `origin`.

### The pivot: Strategy B — alternate go.mod with `-modfile` + replace directives

For each module we want to verify:
1. Parse its `go.mod` into an in-memory modfile structure.
2. For every `require` whose target is a workspace module, add a `replace` directive redirecting to the dep's on-disk path (relative, e.g. `../storage`).
3. Write the result to a sibling file `go.verify.mod` (which Go pairs with `go.verify.sum`).
4. Run `go build -modfile=go.verify.mod ./...` with `GOWORK=off` and `GOFLAGS=-mod=mod`.
5. `defer` removes both alternate files. The real `go.mod` is never touched.

This exercises the rewritten `require` lines against local source. If the
caller got the rewrite wrong (e.g., downstream module calls a symbol the new
upstream doesn't expose), the build fails with a standard Go compile error
that names the missing symbol.

### Environment requirements (document for v1)

- `GOWORK=off` is mandatory. Without it, workspace mode resolves local paths
  and silently swallows the effect of `-modfile`.
- `GOFLAGS=-mod=mod` is mandatory. Without it, `go build` refuses to proceed
  when `go.verify.sum` has no entries for the replaced deps.
- The alt-modfile path is **relative to `cmd.Dir`** and must end in `.mod`.
  Go auto-derives the matching sum path (same stem + `.sum`). Writing an
  empty `go.verify.sum` up-front prevents "missing go.sum entry" errors.

### Working-tree cleanliness

Confirmed. A `git status --porcelain` snapshot taken before `Verify` matches
exactly after `Verify` returns (whether success or failure). `defer os.Remove`
on both alt files reliably cleans up.

### Shifted concern: tag existence is NOT Verify's job

The original plan conflated two concerns under "verify":
1. Does the rewritten go.mod compile against the new upstream source?
2. Will the new upstream version actually exist on the remote when consumers `go get` it?

Strategy B covers (1), which is the hard part — workspace mode hides rewrite
bugs by design. Concern (2) is handled by `git push --atomic` at publish time
(POC-3): either every tag lands or none. There is no need to pre-verify tag
existence.

### Recommendations for v1

- Use Strategy B in `internal/propagate/verify.go`.
- Surface go compile errors verbatim to the user — they already name the
  missing symbol / type mismatch, which is the information the user needs to
  fix the rewrite or their source.

---

## POC-3 — atomic multi-tag push

**Bet:** `git push --atomic origin main <tags...>` truly is all-or-nothing,
including when a server-side pre-receive hook rejects one of the refs.

**Result:** viable as specified.

### Test coverage

- **Happy path:** branch + 3 tags (2 module tags + 1 train tag) all land on the remote.
- **Adversarial path:** pre-receive hook rejects any ref matching `refs/tags/train/*`. After the attempt:
  - Remote has zero of the three test tags (not just no train tag — no module tags either).
  - Remote `main` SHA is unchanged from before the attempt.

### git version tested

`git version 2.50.1 (Apple Git-155)`. `--atomic` has been in git since 2.4 (2015); behavior is stable.

### Recommendations for v1

- Use `git push --atomic origin main <tag-refs>...` as the single publish operation in `internal/propagate/apply.go`.
- On failure, the local state (tags, release commit) is untouched, so `apply` is naturally resumable from the commit + tag step. The user can retry or investigate.
- No server-side infrastructure assumptions needed — atomicity is enforced by the git wire protocol, not by the hosting platform.

---

## Overall assessment

**All three bets cleared. Ready to proceed to v1 implementation plan.**

### Design changes flowing from findings

1. **`modfile.Parse` is the primary data source for the workspace graph** (not `go list`). Documented for `internal/workspace/graph.go`.
2. **Strategy B (`-modfile` + replace) is the verification approach** (not local-tags + `GOPROXY=off`). Documented for `internal/propagate/verify.go`.
3. **Atomic push needs no mitigation** — proceed as originally specified.

### Not-yet-tested concerns (tracked, not blockers)

These were explicitly out of scope for the POCs. Track for v1 or a later POC:
- **v2+ path changes** (`/vN` suffix rewriting across `module` + `require` + imports). Non-trivial but known-solvable.
- **Partial-failure resumability of `apply`** (e.g., release commit succeeded but tag push failed due to transient network error). Current design: local tags are kept, user re-runs `apply`, which should detect existing tags and skip to the push step. Needs a dedicated test.
- **Concurrent propagation race** (two users run `apply` against the same main SHA concurrently). Plan says `apply` requires main's HEAD to match the SHA the plan was computed against; needs to be built and tested.
- **Very large transitive sets** (changing a core module that's imported by hundreds of others). Graph walk is O(E) in the workspace so this scales linearly, but the resulting build verification across hundreds of modules will be sequential and slow — a candidate for parallelization later.
