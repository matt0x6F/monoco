# AGENTS.md

Guidance for AI coding agents working in this repository. Human contributors should read [README.md](README.md) first.

## What monoco is

A coordinator for Go monorepos. It composes `go.work`, `go.mod`, and git tags into atomic, graph-aware releases and task fanout. It is **not** a build system.

## Scoping principles (read before proposing designs)

Monoco's job is to align Go's existing build and release systems for monorepo use — nothing more. The goal is plug-and-play with Go's native tooling.

1. **Derive, don't declare.** If `go.mod`, `go.work`, or git already encodes the fact, monoco reads it. We do not maintain a parallel manifest of the module graph, module paths, versions, or dependencies.
2. **The Go toolchain is the oracle.** `go build`, `go mod`, `go test`, and friends run with their standard semantics. Monoco orchestrates when and where they run; it does not wrap them with new meaning or reinterpret their output.
3. **Tags are the public API.** We produce standard per-module semver tags that `go get` understands natively. No custom resolver, no module proxy, no lockfile, no alternate version format.
4. **Leave-able.** A user who deletes monoco tomorrow keeps a working Go monorepo — standard `go.mod`, standard `go.work`, standard tags. No vestigial files, no broken state, no migration required.
5. **One concept at a time.** New vocabulary (e.g., "release train", "direct-affected") earns its place only when no Go-native term fits. Prefer borrowing Go's words over inventing our own.
6. **Convention beats configuration.** Defaults should be right for the common case so `monoco.yaml` is optional. When configuration is unavoidable, it should *subtract* behavior or *re-parameterize* an existing mechanism — not introduce new ones.

### Corollary: the anti-Bazel stance

We do not invent new build paradigms, override compiler behavior, or require users to declare things the Go toolchain can already discover. A contributor should be able to stop using monoco tomorrow and have a working Go monorepo — their `go.mod`, `go.work`, and tags are all standard.

## Repository layout

- `cmd/monoco/` — CLI entrypoint and subcommand wiring.
- `internal/workspace/` — `go.work` discovery and module enumeration.
- `internal/gitgraph/` — reverse dependency graph derived from `go.mod` requires.
- `internal/affected/` — direct-affected (replace-directive) and cascaded-affected computation.
- `internal/propagate/` — `go.mod` / `go.sum` rewrites; in-process `h1:` hashing.
- `internal/release/` — release-commit / tag / atomic-push orchestration.
- `internal/bump/` — semver bump planning from `--bump` overrides.
- `internal/config/` — `monoco.yaml` loader (optional; sane defaults).
- `internal/tasks/` — test / lint / build / generate fanout over the affected set.
- `internal/fixture/` — local git-repo fixtures for end-to-end tests.
- `docs/` — technical documentation ([architecture](docs/architecture.md), [release model](docs/release-model.md), [POC findings](docs/poc-findings.md)).
- `test/integration/` — integration suite against a real GitHub test monorepo (tag: `integration`).

## Development commands

```bash
go test ./...                                    # unit + local-fixture E2E
go test ./cmd/monoco/... -count=1                # E2E only, no cache
go test -tags=integration ./test/integration/... # real-GitHub integration (needs MONOCO_TEST_REPO_TOKEN)
go build ./cmd/monoco                            # build the binary
go vet ./...                                     # static checks
```

Integration tests require `MONOCO_TEST_REPO_TOKEN` (a PAT with repo scope on the test monorepo) and may mutate real refs — see [test/integration/README.md](test/integration/README.md).

## Conventions agents should follow

- Don't add dependencies lightly; prefer stdlib and `golang.org/x/mod`.
- `go.sum` rewrites must stay deterministic and offline. No network calls during `propagate` or `release`.
- Tag naming follows Go's nested-module convention: `<module-path>/vX.Y.Z` for modules, `train/<date>-<slug>` for release trains. Do not change these without a spec update.
- Errors from the Go toolchain should propagate with context, not be reinterpreted or papered over.
- Significant user-visible behavior changes should update [docs/architecture.md](docs/architecture.md) or [docs/release-model.md](docs/release-model.md) alongside the code.
- `Verify` runs `go build` in module mode (`GOWORK=off`, `GOFLAGS=-mod=mod`) so it catches rewrites workspace mode hides; see [docs/release-model.md](docs/release-model.md).

## Out of scope

See [README.md](README.md) "Not in scope (yet)" — and, more broadly, anything the Go toolchain or git already does correctly.
