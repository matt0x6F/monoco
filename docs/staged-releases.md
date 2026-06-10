# Staged releases (design proposal)

**Status: proposed, not implemented.** This documents the design for releasing
modules that participate in require cycles — the one shape the current atomic
model refuses (see [release-model.md](release-model.md#require-cycles-are-refused)).
It exists so the refusal's eventual replacement is designed next to the
constraint that motivates it.

## The constraint, restated

Two modules that require each other cannot be tagged at the same commit. A
module's zip embeds its own `go.sum`, so A's new zip would have to contain
B's new `h1:` hash while B's contains A's. The hashes are mutually recursive;
no fixed point exists. This is a property of Go's checksum model, and no
release tool can compute its way around it.

Today `release` detects the cycle while topo-ordering the plan and refuses,
naming the members and pointing at `--bump <module>=skip`. That makes the
user the release engineer: ship one side pinned to the other's previous tag,
then ship the other side. Correct, honest — and mechanical enough to automate.

## The insight: the impossibility is per-commit, not per-push

Nothing in the model requires a release to be one commit. Generalize from

> one commit, N tags, one atomic push

to

> an ordered **chain** of commits, tags distributed across them, still **one
> atomic push**.

For a two-module cycle `A ⇄ B`:

```
commit 1  finalize B: its go.mod keeps requiring A@v1.2.3 (the previous tag)
          tag modules/b/v0.4.0 → commit 1

commit 2  rewrite A: require B@v0.4.0 — hashable now, B's content is final
          tag modules/a/v1.3.0 → commit 2
          tag train/<date>-<slug> → commit 2 (the tip)

git push --atomic origin <branch> <all four refs>
```

The hash recursion is broken by commit ordering instead of being unsolvable
within one commit. Atomicity survives at the boundary where it matters — the
publish: consumers see either the whole release or none of it. And the
resulting history is exactly what a careful release engineer produces by hand
(this is how cyclic module pairs in the wild, e.g. the grpc-go ecosystem,
already version their back-edges) — just derived and executed mechanically.

A release therefore becomes a list of **stages**. An acyclic plan is one
stage: byte-for-byte today's behavior. The new vocabulary only appears when a
cycle exists.

## Stage planning

1. Build the condensation of the in-plan require graph (strongly-connected
   components, contracted). The condensation is a DAG; its topo order is the
   stage order. Modules outside any cycle ride whichever stage their
   dependencies complete in.
2. Within an SCC, pick the **cut**: which member releases first, keeping its
   require on the other member(s) pinned to their previous tags.
3. Emit one commit per stage, tags on each stage's commit, train tag on the
   tip, one atomic push of the whole chain.

Rollback semantics are unchanged: every commit and tag is local until the
single push; any pre-push failure restores the pre-run state.

## Cut selection is derived, not declared

The side that releases first ships requiring the *previous* tag of its cycle
partner — so external consumers will compile it against old content, even
though the user's workspace compiled it against new content. That makes cut
selection a **verification question**, which keeps it out of `monoco.yaml`:

- Try each cut direction. A direction is viable iff the first side's new
  content builds in module mode against its partner's *pinned tag content*
  (see below).
- Exactly one viable direction: use it.
- Both viable: prefer the direction that stages the direct-affected module
  last (its consumers see the freshest content), break ties
  deterministically; a `--cut <module>` flag can override.
- **Neither viable:** both new sides need each other's new API
  simultaneously. That is not a tooling failure and must not be papered
  over — tags that don't compile for consumers are worse than no tags. The
  error says exactly that:

  > modules A and B are release-coupled: each side's new code requires the
  > other's new API, so no staged order compiles for external consumers.
  > This cycle is one module pretending to be two — merge them, or break the
  > require cycle in code.

The three exits — auto-staged release, explicit cut, architectural verdict —
replace today's single refusal.

## Pinned-content verification

The new machinery staged releases require. Today's `Verify` redirects every
in-plan require to the dependency's **workspace directory** via `replace`,
which is correct only when the dependency ships in the same commit. A staged
release pins the previous tag, so verification must materialize the
dependency *at that tag* (e.g. `git worktree`/`git archive` into a temp dir)
and point the `replace` there.

This fixes an existing blind spot too: with `--bump <module>=skip`, the
skipped module's consumers are verified against its workspace dir today, not
against the old tag their rewritten `go.mod` actually pins. Same machinery,
same fix.

## User experience

One command, one confirmation. The plan grows a stage column only when
staging is needed:

```
Plan:
  Train: train/2026-06-10-auth-refactor

  STAGE  MODULE                    OLD     NEW     KIND   DIRECT
  1      modules/users             v0.3.2  v0.3.3  patch  cascade   (pins auth v1.2.3 — previous)
  2      modules/auth              v1.2.3  v1.3.0  minor  direct    (pins users v0.3.3)
  2      modules/gateway           v0.9.0  v0.9.1  patch  cascade
```

## Scope check

Against the scoping principles in [AGENTS.md](../AGENTS.md):

- **Derive, don't declare** — stages and cuts come from the require graph and
  verification, not configuration. `--cut` is an override, never a requirement.
- **Tags are the public API** — unchanged. Per-module semver tags, each
  pointing at a commit where that module's content is final and consistent.
- **Leave-able** — the history is indistinguishable from a hand-rolled
  staggered release.
- **One concept at a time** — "stage" is the single addition, visible only
  when a cycle forces it.

## Open questions

- Verification cost: each candidate cut direction is a module-mode build of
  the first stage; a two-member cycle costs at most two extra builds. Fine.
  Pathological many-SCC plans could multiply this — probably acceptable to
  verify only the chosen order and fail late.
- Interaction with major bumps: a `/vN` crossing inside a cycle changes the
  partner's import paths; stage 1 cannot pin "previous tag" across a path
  rename. Likely refuse major bumps within an SCC initially.
- Pushes with chained commits assume the base branch hasn't advanced
  mid-apply; the existing `--force-with-lease` + base-SHA preflight already
  covers this.
