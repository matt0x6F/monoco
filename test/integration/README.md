# Integration tests

These tests drive `monoco` against a real GitHub-hosted test monorepo
([`matt0x6F/monoco-test-monorepo`](https://github.com/matt0x6F/monoco-test-monorepo))
and assert real behavior: tags land on `origin`, an external consumer
can `go get` a newly produced tag, verification failures roll back
cleanly, etc. They complement the in-process unit tests under
`internal/...`, which use a local-git fixture and never touch the network.

Build-tagged `integration`, so they're invisible to `go test ./...`.
You must pass `-tags=integration` explicitly.

## Running locally

```bash
MONOCO_TEST_REPO_TOKEN=<PAT> \
MONOCO_TEST_REPO_HTTPS=https://github.com/matt0x6F/monoco-test-monorepo.git \
  go test -tags=integration ./test/integration/... -v -count=1 -timeout=15m
```

The PAT needs `contents:write` on `monoco-test-monorepo`. A fine-grained
token scoped to that single repo is strongly preferred.

Without a token, the harness falls back to SSH (`git@github.com:...`);
works on your dev machine if your SSH agent is set up, not in CI.

## Running in CI

Handled by `.github/workflows/integration.yml`. It fires on pushes to
`main` and `workflow_dispatch`. Fork PRs do not run this — unit tests in
`ci.yml` are the PR gate. Integration is a post-merge confidence check.

The `integration-sweep.yml` workflow deletes stale `test/*` branches and
`modules/*/v*` / `train/*` tags older than 7 days, nightly. Run manually
with `dry_run=true` first if you're uncertain.

## Scenario matrix

| Test | Purpose |
|---|---|
| `TestFeatEndToEnd` | Flagship: plan → apply → remote tags → external consumer `go get` |
| `TestNoChangeIsNoOp` | Empty range produces no plan, no push |
| `TestFixCommitIsPatchBump` | Default patch bump (no `--bump` override); cascades as patch |
| `TestBreakingChangeOnV0` | `--bump=major` on a v0 module stays within v0 (pre-1.0 rule) |
| `TestMultiModuleFeat` | Two direct changes + cascade, all tagged on one release commit |
| `TestVerificationFailureRollsBack` | Broken api.go → apply fails → no tags, no release commit |
| `TestPlanOnlyDoesNotMutate` | `release --dry-run` is read-only |

## Adding a scenario

1. Create `test/integration/<scenario>_test.go` with `//go:build integration`.
2. `h := newHarness(t)` — clones, branches, builds the CLI.
3. Make commits via `h.writeFeat` / `h.writeFix` / `h.writeBreaking` /
   `h.writeBreakingAPI`. The commit-message prefixes are cosmetic —
   bumps are declared at release time via `--bump <module>=<kind>`, not
   inferred from commit messages.
4. Call `h.addLocalReplace(<target>)` to install the workspace-local
   `replace` directive that marks the target module as direct-affected.
5. Call `h.plan(...)` / `h.apply(...)` with any `--bump` overrides your
   scenario needs, and assert on stdout plus remote state via
   `h.assertRemoteHasTag` / `h.assertRemoteMissingTag`.

Each test's branch and tags are namespaced with a unique `runID`
(timestamp + 6 hex chars), so scenarios don't collide with each other
or with concurrent runs.

## Interpreting a failure

| Symptom | Most likely cause |
|---|---|
| `preflight ... ls-remote failed` | Token invalid/expired, or network blip. Check secret. |
| Single test flakes; re-run passes | Transient network; `mustRunRetry` already retries clone/ls-remote. If recurring, widen the retry set. |
| `plan entry for ... not found` | Plan output format changed (`cmd/monoco/commands.go:printPlan`). Update `findPlanEntry`. |
| `apply did not push` | Verify step failed — look at stderr for the module-mode compile error. |
| `consumer \`go get\` ... failed` | Tag didn't propagate to the module proxy yet (shouldn't happen with `GOPROXY=direct`), or the new tag's `go.mod` is malformed. |
| Remote branches/tags piling up | `integration-sweep.yml` isn't running; trigger it manually. |
