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

## Fixture caveat (read before interpreting POC-1 and POC-2)

The fixture generator models the **bootstrap state** of a brand-new monorepo
that has never been released: module paths are `example.com/mono/<name>`
(not resolvable to any real VCS host), and cross-module `require` lines
use the placeholder pseudo-version `v0.0.0-00010101000000-000000000000`.

In monoco's **steady-state** operation (after the first propagation), these
two properties both change: module paths point at the real host (e.g.
`github.com/org/monorepo/modules/storage`), and cross-module `require`
lines reference real tags (`v0.2.0`) that exist on the remote.

This caveat matters for two of the POCs:

- **POC-1:** my initial write-up claimed `go list -m -json all` couldn't be
  used because it failed to resolve placeholder pseudo-versions. That
  failure mode is a bootstrap-only artifact — in steady state, `go list`
  would work fine against real tagged versions. The pivot to `modfile.Parse`
  still stands (see POC-1 below for the *real* reasons), but not because
  `go list` is broken in general.
- **POC-2:** Strategy A (local tags + `GOPROXY=direct`) would likely work
  for a real monorepo whose module paths resolve to a real VCS host (Go's
  direct-mode resolver does `git ls-remote` against the declared host).
  The fixture's `example.com/mono/*` paths are not resolvable by *any*
  mechanism, so Strategy A couldn't be meaningfully tested here. Strategy B
  was chosen as the v1 approach because it works in both cases; Strategy A
  is not "broken," just not validated by this fixture.

Properly validating Strategy A would require either a real hosted git
remote (GitHub/GitLab) or a local `git-http-backend` serving the meta-tag
protocol — both beyond POC scope. This is tracked in "Not-yet-tested
concerns" below.

---

## POC-1 — reverse-dep graph from `go.work` + go.mod files

**Bet:** we can compute transitive reverse-dep affected sets across workspace
modules without parsing Go source, in a sub-second walk.

**Result:** viable.

### Finding: `modfile.Parse` is the right primary data source

The plan proposed running `go list -m -json all` (with `GOWORK=off`) in each
workspace module to enumerate its dependencies. For monoco's use case —
computing the *workspace-internal* reverse-dep graph — this is the wrong tool:

- **Needless indirection:** `go list` resolves the full transitive dep graph
  including external modules. monoco only cares about edges where both
  endpoints are workspace modules. Reading each `go.mod` directly with
  `modfile.Parse` gives exactly that, with no extra work.
- **Subprocess fanout cost:** `go list` spawns a process per module.
  `modfile.Parse` is in-process. At 50 modules this is the difference
  between ~1ms total and hundreds of ms of fork+parse overhead.
- **New failure modes for no benefit:** `go list` can fail in ways
  `modfile.Parse` can't — unresolvable `require` lines (during bootstrap
  before any tags exist), proxy/network errors, toolchain mismatches.
  Accepting those failure modes buys us nothing we need.

The fixture used here uses placeholder pseudo-versions (bootstrap state),
which makes `go list` fail outright. That's a fixture artifact — in steady
state, `go list` would work. But the three reasons above stand independent
of the fixture: we don't *want* the information `go list` gives us beyond
what `modfile.Parse` already provides.

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

### Finding: Strategy B is the right verification approach for v1

The plan proposed two approaches. The fixture forced Strategy B, but the
underlying reasoning is more nuanced than "Strategy A doesn't work":

- **Strategy A (local tags + `GOPROXY=direct`)** likely works for a real
  monorepo whose module paths resolve to a real VCS host. Go's direct-mode
  resolver does `git ls-remote <host>` to find tags, so a local tag that
  matches the expected `<subpath>/v<X.Y.Z>` convention should satisfy
  `go build` when the remote `origin` points to that host. This could not
  be validated against the fixture (module paths are `example.com/mono/*`,
  not a real host), and validating it properly needs a real hosted git
  remote or a local `git-http-backend` — both beyond POC scope.
- **Strategy B (`-modfile=<alt>` + replace directives)** works
  *unconditionally*. It doesn't care about module paths, proxies, hosts,
  or tag resolution. It exercises the rewritten `require` lines against
  local source via `replace` redirection.

Strategy B wins for v1 on three grounds:
1. **Works for all monorepos**, including those with internal or unhosted
   module paths.
2. **No network dependency**, so verification is fast and works offline.
3. **Pure compile check** — surfaces exactly the failure we need to catch
   (rewritten require line is incoherent with downstream source).

Strategy A remains a legitimate fallback if we later want richer
verification (e.g., simulating external-consumer resolution behavior),
but Strategy B covers the use case that blocks a broken publish.

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
- **Steady-state fixture** — validate POC-1 and POC-2 against a fixture that uses real module paths (resolvable via a local `git-http-backend` or a test GitHub repo) and real tagged versions. Would confirm the pivots against realistic conditions and open up testing Strategy A. Not a blocker — the pivots stand on other grounds — but good hygiene before trusting monoco in production.
- **v2+ path changes** (`/vN` suffix rewriting across `module` + `require` + imports). Non-trivial but known-solvable.
- **Partial-failure resumability of `apply`** (e.g., release commit succeeded but tag push failed due to transient network error). Current design: local tags are kept, user re-runs `apply`, which should detect existing tags and skip to the push step. Needs a dedicated test.
- **Concurrent propagation race** (two users run `apply` against the same main SHA concurrently). Plan says `apply` requires main's HEAD to match the SHA the plan was computed against; needs to be built and tested.
- **Very large transitive sets** (changing a core module that's imported by hundreds of others). Graph walk is O(E) in the workspace so this scales linearly, but the resulting build verification across hundreds of modules will be sequential and slow — a candidate for parallelization later.
- **Bootstrap case** — the very first propagation of a brand-new monorepo, where cross-module requires carry placeholder pseudo-versions. Handled naturally by `modfile.Parse` (which doesn't care about resolvability), and the first propagation converts placeholders to real versions. Worth an explicit test in v1.
