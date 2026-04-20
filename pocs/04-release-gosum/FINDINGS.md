# POC 04 — Release-time go.sum population

**Date:** 2026-04-19
**Environment:** go1.25.0 darwin/arm64, git 2.50.1
**Context:** Blocker for issue #22 (`monoco release`). Today's `propagate apply`
rewrites `require` lines only — no `replace` stripping, no `go.sum`
maintenance. A consumer checking out the resulting tag cannot build.
This spike was to decide how `release` should populate `go.sum` for
freshly-tagged (not yet pushed) modules.

## Summary

| Strategy | Verdict |
|---|---|
| A. `GOPROXY=direct` + git `insteadOf` against a bare remote | ❌ Not viable |
| B. Filesystem `GOPROXY` serving a pre-built module zip | ✅ Works |
| C. Precompute `h1:` hashes with `dirhash`; write `go.sum` directly | ✅ Works (preferred) |

## Details

### Strategy A — direct mode with `insteadOf` redirection

Go's direct-mode resolver unconditionally issues
`https://<host>/<path>?go-get=1` to discover the VCS URL via meta tags.
`git config url."file://...".insteadOf "https://..."` only intercepts git
operations — not the initial HTTP discovery. Result:

```
unrecognized import path "example.local/mono/foo": https fetch:
Get "https://example.local/mono/foo?go-get=1": dial tcp: lookup example.local: no such host
```

Making this work requires either real DNS + HTTPS meta-tag serving (beyond
monoco's scope) or `GOVCS` + a resolvable host. For a release flow that must
work offline and before pushing, this is a non-starter.

### Strategy B — filesystem GOPROXY

Canonical proxy layout under `$proxy/example.local/mono/foo/@v/`:
`list`, `v1.1.0.info`, `v1.1.0.mod`, `v1.1.0.zip`. With
`GOPROXY=file://$proxy` the build succeeds and `go.sum` is populated
exactly as we'd expect:

```
example.local/mono/foo v1.1.0 h1:H9gkMeLpQStgsq+58cixmBJJPdxLk+0zDWWjNststnc=
example.local/mono/foo v1.1.0/go.mod h1:ZvhfqdIJzlR0dpixrliOTCEoNVNAYNjuY7qWJFanM/M=
```

Works, but adds state: we must materialise a proxy dir for every tag in a
release, and the consumer build still has to discover it. For monoco's
purposes this is unnecessary machinery.

### Strategy C — precompute hashes, write `go.sum` directly

Given a clean checkout of the tagged tree, the two `h1:` lines are
deterministic:

```go
// zip hash
z, _ := os.Create(outZip)
modzip.CreateFromDir(z, module.Version{Path: mp, Version: ver}, dir)
h1, _ := dirhash.HashZip(outZip, dirhash.Hash1)

// go.mod hash
h1mod, _ := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
    return os.Open(filepath.Join(dir, "go.mod"))
})
```

Both values match what a `go build` via filesystem GOPROXY produces.
Writing them into `go.sum` and building with `GOPROXY=off` succeeds **if
the module cache has the zip** — the spike shows this works when the
cache was primed by a prior Strategy-B run.

For monoco, the module cache is NOT primed at release time. But we don't
need to build against the tagged version to verify — `internal/propagate`
already has `Verify` which builds each module with an alt `go.verify.mod`
that rewrites in-workspace requires to local `replace ./...` paths. No
network, no cache, no proxy.

## Implications for `monoco release`

1. **No network at release time.** Compute `h1:` lines ourselves with
   `golang.org/x/mod/zip` + `golang.org/x/mod/sumdb/dirhash` (~40 LOC).
2. **No tag-then-push ordering puzzle.** Everything — rewrite, hash,
   verify, commit, tag — happens locally. Push is atomic at the end.
3. **Rollback stays trivial.** If verify fails, `git reset --hard` on the
   snapshotted HEAD. Nothing is pushed until the last step.
4. **Verify path unchanged.** Keep the existing alt-modfile trick in
   `internal/propagate/verify.go`. It already validates the module set
   against local workspace copies, which is exactly what we want.
5. **Replace stripping can be done at rewrite time.** Same
   `modfile.Parse` + `mf.DropReplace` path as the existing require-rewrites.

## What to build

In `internal/propagate` (or a new `internal/gosum` package if we prefer
the separation):

- `ComputeModuleHashes(modDir, modPath, version) (h1, h1mod string, err)` —
  thin wrapper around `modzip.CreateFromDir` + `dirhash`.
- Extend `ComputeRewrites` to also drop workspace-local `replace`
  directives and emit `go.sum` additions per downstream.
- Apply writes both `go.mod` and `go.sum` before commit.

## Spike artefacts

- `spike.sh` — end-to-end reproduction (setup, bump, strategies B/C).
- Tmp dirs preserved at run time for inspection.
