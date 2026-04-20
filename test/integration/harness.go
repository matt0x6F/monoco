//go:build integration
// +build integration

// Package integration drives monoco against a real GitHub-hosted test
// monorepo. The harness here sets up an isolated clone per test, wires
// up HTTPS-token auth if available, builds the monoco binary once, and
// exposes helpers for making conventional-commit changes and invoking
// `monoco propagate {plan,apply}`.
//
// Run with:
//
//	go test -tags=integration ./test/integration/... -v -count=1 -timeout=15m
//
// Env:
//
//	MONOCO_TEST_REPO_HTTPS  HTTPS URL of the test repo (preferred for CI).
//	MONOCO_TEST_REPO_SSH    SSH URL (fallback for local dev).
//	MONOCO_TEST_REPO_TOKEN  PAT with contents:write on the test repo
//	                        (required when using HTTPS in CI).
//	MONOCO_TEST_REPO_MOD    module-path prefix override.
//
// Each run namespaces its branch and tags with a timestamp+hex runID so
// concurrent runs accumulate harmless history rather than racing.
package integration

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	defaultRepoHTTPS = "https://github.com/matt0x6F/monoco-test-monorepo.git"
	defaultRepoSSH   = "git@github.com:matt0x6F/monoco-test-monorepo.git"
	defaultModPath   = "github.com/matt0x6F/monoco-test-monorepo"
)

// buildOnce caches the compiled monoco binary across tests in the same
// `go test` invocation. Build costs ~2s; with 7 scenarios that matters.
var (
	buildOnce   sync.Once
	builtBin    string
	buildErr    error
)

type harness struct {
	t        *testing.T
	bin      string // path to built monoco binary
	wt       string // working tree (clone)
	runID    string // timestamp+hex, used for branch and slug
	branch   string // "test/<runID>"
	modPath  string // module-path prefix (e.g. github.com/matt0x6F/monoco-test-monorepo)
	base     string // merge-base of HEAD with origin/main, set after branch
	cloneURL string // URL actually used for clone+push (token-baked if HTTPS+TOKEN)

	// baselineTags is a snapshot of refs/tags/* on origin at harness
	// creation, mapping ref → SHA. Used by assertRemoteMissingTag to
	// distinguish "this run pushed the tag" (fail) from "a prior run
	// legitimately left this tag behind" (ignore). Module tags like
	// modules/storage/v0.2.0 are global and accumulate across runs
	// until the nightly sweep GCs them, so bare presence/absence is
	// not a sufficient signal.
	baselineTags map[string]string
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	requireBin(t, "git")
	requireBin(t, "go")

	modPath := envOr("MONOCO_TEST_REPO_MOD", defaultModPath)
	cloneURL, authNote := chooseCloneURL()
	t.Logf("test repo auth: %s", authNote)

	// Preflight: if the repo isn't reachable, fail fast with a clear
	// message before we build the CLI or touch anything.
	preflight := exec.Command("git", "ls-remote", "--heads", cloneURL)
	preflight.Env = sanitizedEnv()
	if out, err := preflight.CombinedOutput(); err != nil {
		t.Fatalf("preflight `git ls-remote %s` failed — test repo unreachable or auth broken:\n%s\n%v",
			redact(cloneURL), out, err)
	}

	bin := buildMonocoOnce(t)
	runID := runIDNow(t)
	branch := "test/" + runID

	wt := t.TempDir()
	mustRunRetry(t, "", 3, "git", "clone", "--depth", "50", cloneURL, wt)
	mustRun(t, wt, "git", "config", "user.email", "integration-test@monoco.example")
	mustRun(t, wt, "git", "config", "user.name", "monoco integration test")
	// Shallow clone only brings tags reachable from fetched history.
	// Module tags from prior runs point at per-run commits that aren't
	// on main, so they'd be invisible locally — causing monoco to plan
	// a bump onto a version that already exists remotely and then get
	// rejected by atomic push. Fetch all tags explicitly so the local
	// tag state matches the remote's public API.
	mustRunRetry(t, wt, 3, "git", "fetch", "--tags", "origin")
	mustRun(t, wt, "git", "checkout", "-b", branch)

	base := trim(mustCapture(t, wt, "git", "merge-base", "HEAD", "origin/main"))

	h := &harness{
		t:            t,
		bin:          bin,
		wt:           wt,
		runID:        runID,
		branch:       branch,
		modPath:      modPath,
		base:         base,
		cloneURL:     cloneURL,
		baselineTags: snapshotRemoteTags(t, wt),
	}
	t.Logf("harness ready: runID=%s branch=%s base=%s baselineTags=%d",
		runID, branch, base[:min(12, len(base))], len(h.baselineTags))
	return h
}

// snapshotRemoteTags returns a map of refs/tags/* → SHA on origin at
// the moment of the call. Used to detect which tags a test run pushed
// vs. which already existed (since module tags are globally namespaced
// and prior runs' tags linger until the nightly sweep GCs them).
func snapshotRemoteTags(t *testing.T, wt string) map[string]string {
	t.Helper()
	out := mustCapture(t, wt, "git", "ls-remote", "--refs", "origin", "refs/tags/*")
	m := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 2 {
			m[parts[1]] = parts[0]
		}
	}
	return m
}

// chooseCloneURL returns (url, human-readable note). Prefers HTTPS+token;
// falls back to SSH URL for local dev.
func chooseCloneURL() (string, string) {
	token := os.Getenv("MONOCO_TEST_REPO_TOKEN")
	if token != "" {
		base := envOr("MONOCO_TEST_REPO_HTTPS", defaultRepoHTTPS)
		// Bake the token into the clone URL. Because the URL lives
		// only in the workspace's .git/config inside t.TempDir(), it
		// is cleaned up automatically when the test ends.
		withAuth := strings.Replace(base, "https://", "https://x-access-token:"+token+"@", 1)
		return withAuth, "HTTPS with MONOCO_TEST_REPO_TOKEN"
	}
	ssh := envOr("MONOCO_TEST_REPO_SSH", defaultRepoSSH)
	return ssh, "SSH (" + ssh + ") — no token provided"
}

// Commit helpers. Each appends a unique-per-run symbol so repeated runs
// always have something to commit (the test repo's main doesn't move).

func (h *harness) writeFeat(module, symbol string) {
	h.writeCommit(module, symbol, "feat("+module+"): add "+symbol+" for run "+h.runID)
}

func (h *harness) writeFix(module, symbol string) {
	h.writeCommit(module, symbol, "fix("+module+"): tweak "+symbol+" for run "+h.runID)
}

func (h *harness) writeBreaking(module, symbol string) {
	h.writeCommit(module, symbol, "feat("+module+")!: breaking "+symbol+" for run "+h.runID)
}

// writeCommit appends a new exported symbol to the module's main .go file
// so the commit is non-empty and compiles cleanly.
func (h *harness) writeCommit(module, symbol, msg string) {
	h.t.Helper()
	path := filepath.Join(h.wt, "modules", module, module+".go")
	existing, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatalf("read %s: %v", path, err)
	}
	added := fmt.Sprintf("\n// %s is introduced by integration run %s.\nfunc %s() string { return %q }\n",
		symbol, h.runID, symbol, h.runID)
	if err := os.WriteFile(path, append(existing, []byte(added)...), 0o644); err != nil {
		h.t.Fatalf("write %s: %v", path, err)
	}
	mustRun(h.t, h.wt, "git", "add", "-A")
	mustRun(h.t, h.wt, "git", "commit", "-m", msg)
}

// writeBreakingAPI makes api.go reference a symbol on storage that
// doesn't exist — used to force the verify step to fail.
func (h *harness) writeBreakingAPI() {
	h.t.Helper()
	path := filepath.Join(h.wt, "modules/api/api.go")
	existing, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatalf("read %s: %v", path, err)
	}
	added := fmt.Sprintf("\n// Broken is deliberately broken by integration run %s.\nfunc Broken() string { return storage.DoesNotExist_%s() }\n",
		h.runID, strings.ReplaceAll(h.runID, "-", "_"))
	if err := os.WriteFile(path, append(existing, []byte(added)...), 0o644); err != nil {
		h.t.Fatalf("write %s: %v", path, err)
	}
	mustRun(h.t, h.wt, "git", "add", "-A")
	mustRun(h.t, h.wt, "git", "commit", "-m", "feat(api): reference missing storage symbol (run "+h.runID+")")
}

// addLocalReplace appends a workspace-local `replace` from api/go.mod to
// the named target module and commits it. Required because `monoco
// release` derives the direct-affected set from replace directives.
func (h *harness) addLocalReplace(target string) {
	h.t.Helper()
	apiMod := filepath.Join(h.wt, "modules/api/go.mod")
	b, err := os.ReadFile(apiMod)
	if err != nil {
		h.t.Fatalf("read %s: %v", apiMod, err)
	}
	line := fmt.Sprintf("\nreplace %s/modules/%s => ../%s\n", h.modPath, target, target)
	if err := os.WriteFile(apiMod, append(b, []byte(line)...), 0o644); err != nil {
		h.t.Fatalf("write %s: %v", apiMod, err)
	}
	mustRun(h.t, h.wt, "git", "add", "-A")
	mustRun(h.t, h.wt, "git", "commit", "-m", "local: replace "+target+" for run "+h.runID)
}

// plan runs `monoco release --dry-run --slug <runID>` and returns stdout.
// It fails the test on non-zero exit.
func (h *harness) plan(extra ...string) string {
	h.t.Helper()
	args := append([]string{"release", "--dry-run", "--slug", h.runID}, extra...)
	return mustCapture(h.t, h.wt, h.bin, args...)
}

// apply runs `monoco release -y --remote origin --slug <runID>`.
func (h *harness) apply(extra ...string) string {
	h.t.Helper()
	args := append([]string{"release", "-y", "--slug", h.runID, "--remote", "origin"}, extra...)
	return mustCapture(h.t, h.wt, h.bin, args...)
}

// applyExpectFail runs release and returns (stdout, stderr) if the
// command FAILED, or fails the test if it succeeded.
func (h *harness) applyExpectFail(extra ...string) (string, string) {
	h.t.Helper()
	args := append([]string{"release", "-y", "--slug", h.runID, "--remote", "origin"}, extra...)
	cmd := exec.Command(h.bin, args...)
	cmd.Dir = h.wt
	cmd.Env = sanitizedEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		h.t.Fatalf("expected apply to fail, but it succeeded\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String()
}

// remoteTagsForRun lists refs created by THIS run (filtered by runID).
// Returns refs like ["refs/tags/modules/storage/v0.2.0", ...].
func (h *harness) remoteTagsForRun() []string {
	h.t.Helper()
	// train/<date>-<runID> plus any modules/*/vX that were created on
	// the release commit. Listing by runID is unreliable (tag names
	// don't contain it), so list all tags reachable from the branch
	// on origin instead.
	out := mustCapture(h.t, h.wt, "git", "ls-remote", "--refs", "origin", "refs/tags/train/*-"+h.runID)
	var refs []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		// sha \t refname
		parts := strings.Fields(line)
		if len(parts) == 2 {
			refs = append(refs, parts[1])
		}
	}
	return refs
}

// assertRemoteHasTag fails the test if the given tag ref isn't present
// on origin.
func (h *harness) assertRemoteHasTag(ref string) {
	h.t.Helper()
	out := mustCapture(h.t, h.wt, "git", "ls-remote", "--refs", "origin", ref)
	if !strings.Contains(out, ref) {
		h.t.Errorf("remote missing %s\nls-remote output:\n%s", ref, out)
	}
}

// assertReleaseCommitTouches fails the test if the local HEAD (which
// after `apply` is the release commit) does not include a change to
// modules/<module>/<file>. Use this to lock in that downstream go.sum
// files actually land in the commit — a regression in the go.sum
// writing path is otherwise silent: the build still succeeds via the
// proxy populating go.sum lazily, but offline/readonly consumers
// break.
func (h *harness) assertReleaseCommitTouches(module, file string) {
	h.t.Helper()
	rel := "modules/" + module + "/" + file
	out := mustCapture(h.t, h.wt, "git", "show", "--name-only", "--pretty=format:", "HEAD")
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == rel {
			return
		}
	}
	h.t.Errorf("release commit does not touch %s\nfiles changed:\n%s", rel, out)
}

// assertRemoteMissingTag fails the test if `ref` was created or moved
// during this run. A tag that existed at the same SHA when the harness
// started is treated as pre-existing (from a prior run) and ignored —
// module tags are a shared namespace and we can only attribute a push
// to this run when the SHA differs from baseline.
func (h *harness) assertRemoteMissingTag(ref string) {
	h.t.Helper()
	out := mustCapture(h.t, h.wt, "git", "ls-remote", "--refs", "origin", ref)
	var sha string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == ref {
			sha = parts[0]
			break
		}
	}
	if sha == "" {
		return
	}
	if baseline := h.baselineTags[ref]; sha == baseline {
		return
	}
	h.t.Errorf("remote unexpectedly has %s at %s (baseline: %q) — this run pushed or moved the tag\nls-remote output:\n%s",
		ref, sha, h.baselineTags[ref], out)
}

// consumerProbe builds a throwaway consumer module in a temp dir and
// runs `go get <modPath>/modules/api@<version>` followed by `go build`.
// This is the highest-confidence check that monoco-produced tags are
// externally consumable.
func (h *harness) consumerProbe(apiVersion string) {
	h.t.Helper()
	consumerDir := h.t.TempDir()
	consumerHome := h.t.TempDir() // sandboxed HOME for .netrc

	// Go populates $GOMODCACHE (default $HOME/go/pkg/mod) with files
	// mode 0444 so module contents are immutable. t.TempDir's cleanup
	// RemoveAll doesn't chmod before unlinking, so teardown would fail
	// with "permission denied." Register a LIFO-earlier cleanup that
	// walks the dir and restores write perms. (t.Cleanup fires before
	// the RemoveAll registered by TempDir at creation.)
	h.t.Cleanup(func() {
		_ = filepath.Walk(consumerHome, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			_ = os.Chmod(p, 0o700)
			return nil
		})
	})

	consumerGoMod := "module monoco-integration-consumer\n\ngo 1.22\n"
	consumerMain := "package main\n\nimport (\n\t\"fmt\"\n\n\t\"" +
		h.modPath + "/modules/api\"\n)\n\nfunc main() {\n\tfmt.Println(api.Fetch(\"probe\"))\n}\n"
	mustWrite(h.t, filepath.Join(consumerDir, "go.mod"), consumerGoMod)
	mustWrite(h.t, filepath.Join(consumerDir, "main.go"), consumerMain)

	// If we have a token, write a .netrc so `go get` (which shells out
	// to git) can auth against github.com without baking the token
	// into a URL that might get logged.
	if token := os.Getenv("MONOCO_TEST_REPO_TOKEN"); token != "" {
		netrc := "machine github.com login x-access-token password " + token + "\n"
		mustWrite(h.t, filepath.Join(consumerHome, ".netrc"), netrc)
		if err := os.Chmod(filepath.Join(consumerHome, ".netrc"), 0o600); err != nil {
			h.t.Fatal(err)
		}
	}

	env := append(sanitizedEnv(),
		"HOME="+consumerHome,
		"GOWORK=off",
		"GOPROXY=direct",
		"GOSUMDB=off",
		"GOFLAGS=-mod=mod",
	)

	get := exec.Command("go", "get", h.modPath+"/modules/api@"+apiVersion)
	get.Dir = consumerDir
	get.Env = env
	if out, err := get.CombinedOutput(); err != nil {
		h.t.Fatalf("consumer `go get %s/modules/api@%s` failed: %v\n%s",
			h.modPath, apiVersion, err, out)
	}

	build := exec.Command("go", "build", "./...")
	build.Dir = consumerDir
	build.Env = env
	if out, err := build.CombinedOutput(); err != nil {
		h.t.Fatalf("consumer `go build` against new api tag failed: %v\n%s", err, out)
	}
}

// ---- generic helpers ----

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func requireBin(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Fatalf("required binary %q not on PATH: %v", name, err)
	}
}

func buildMonocoOnce(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "monoco-bin-*")
		if err != nil {
			buildErr = err
			return
		}
		bin := filepath.Join(dir, "monoco")
		cmd := exec.Command("go", "build", "-o", bin, "github.com/matt0x6f/monoco/cmd/monoco")
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("build monoco: %v\n%s", err, out)
			return
		}
		builtBin = bin
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return builtBin
}

func runIDNow(t *testing.T) string {
	t.Helper()
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
}

func todayUTC() string { return time.Now().UTC().Format("2006-01-02") }

// sanitizedEnv strips any git/auth env vars from the parent process
// that could interfere with our controlled token handling. We do NOT
// strip GOPATH/GOCACHE because the test binary needs them.
func sanitizedEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_ASKPASS=") || strings.HasPrefix(kv, "SSH_AUTH_SOCK=") {
			// Keep SSH_AUTH_SOCK when we don't have a token — SSH
			// fallback path needs it. Strip GIT_ASKPASS always.
			if strings.HasPrefix(kv, "GIT_ASKPASS=") {
				continue
			}
			if os.Getenv("MONOCO_TEST_REPO_TOKEN") != "" {
				continue
			}
		}
		out = append(out, kv)
	}
	return out
}

// mustRun runs name+args in dir and fails the test on non-zero exit.
func mustRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = sanitizedEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, redactArgs(args), err, out)
	}
}

// mustRunRetry wraps mustRun with exponential backoff for transient
// network errors (DNS / RPC failures / timeouts). Auth failures and
// non-fast-forward pushes are NOT retried.
func mustRunRetry(t *testing.T, dir string, attempts int, name string, args ...string) {
	t.Helper()
	var lastOut []byte
	var lastErr error
	backoff := 2 * time.Second
	for i := 0; i < attempts; i++ {
		cmd := exec.Command(name, args...)
		if dir != "" {
			cmd.Dir = dir
		}
		cmd.Env = sanitizedEnv()
		out, err := cmd.CombinedOutput()
		if err == nil {
			return
		}
		lastOut, lastErr = out, err
		if !isTransientGitErr(string(out)) {
			break
		}
		t.Logf("attempt %d/%d for %s %v failed transiently; retrying in %s",
			i+1, attempts, name, redactArgs(args), backoff)
		time.Sleep(backoff)
		backoff *= 2
	}
	t.Fatalf("%s %v failed after retries: %v\n%s", name, redactArgs(args), lastErr, lastOut)
}

var transientPatterns = []string{
	"Could not resolve host",
	"RPC failed",
	"Connection timed out",
	"early EOF",
	"unexpected disconnect",
	"TLS handshake",
	"connection reset by peer",
}

func isTransientGitErr(out string) bool {
	for _, p := range transientPatterns {
		if strings.Contains(out, p) {
			return true
		}
	}
	return false
}

// mustCapture is mustRun but returns stdout.
func mustCapture(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = sanitizedEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %v: %v\nstdout:\n%s\nstderr:\n%s",
			name, redactArgs(args), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func trim(s string) string { return strings.TrimSpace(s) }

// findPlanEntry scans a plan-table printout for the given module path
// and returns its (old, new, kind, direct) columns. Matches format
// emitted by cmd/monoco/commands.go:printPlan.
func findPlanEntry(t *testing.T, planOut, modulePath string) (oldV, newV, kind, direct string) {
	t.Helper()
	for _, line := range strings.Split(planOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != modulePath {
			continue
		}
		return fields[1], fields[2], fields[3], fields[4]
	}
	t.Fatalf("plan entry for %s not found in:\n%s", modulePath, planOut)
	return "", "", "", ""
}

// tokenRegexp matches the baked-in token in an HTTPS URL so we can
// redact it from error messages and logs.
var tokenRegexp = regexp.MustCompile(`https://x-access-token:[^@]+@`)

func redact(s string) string {
	return tokenRegexp.ReplaceAllString(s, "https://x-access-token:REDACTED@")
}

func redactArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = redact(a)
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
