//go:build integration
// +build integration

// Package integration runs monoco against a real GitHub-hosted test monorepo
// to validate the last remaining confidence gap: that monoco-produced tags
// are consumable via normal `go get` by an external consumer.
//
// Run with:
//   go test -tags=integration ./test/integration/... -v -count=1
//
// Env:
//   MONOCO_TEST_REPO_SSH   SSH URL of the test repo (default: the monoco-test-monorepo under matt0x6F).
//   MONOCO_TEST_REPO_MOD   module-path prefix (default: github.com/matt0x6F/monoco-test-monorepo).
//
// Each run creates a unique branch and set of tags (namespaced by a timestamp
// + short random ID) so repeated runs accumulate harmless history rather than
// racing. Nothing on `main` is disturbed.
package integration

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	defaultRepoSSH = "git@github.com:matt0x6F/monoco-test-monorepo.git"
	defaultModPath = "github.com/matt0x6F/monoco-test-monorepo"
)

func TestMonocoAgainstGitHub_endToEnd(t *testing.T) {
	repoSSH := envOr("MONOCO_TEST_REPO_SSH", defaultRepoSSH)
	modPath := envOr("MONOCO_TEST_REPO_MOD", defaultModPath)

	// Pre-flight: must have git and go.
	requireBin(t, "git")
	requireBin(t, "go")

	// Unique run ID: YYYYMMDDTHHMMSS-<6 hex chars>. Used for branch and slug.
	runID := runIDNow(t)
	testBranch := "test/" + runID

	// 1. Build the monoco CLI from the repo under test.
	mono := buildMonoco(t)

	// 2. Clone the test monorepo to a temp working tree.
	wt := t.TempDir()
	mustRun(t, "", "git", "clone", "--depth", "50", repoSSH, wt)
	mustRun(t, wt, "git", "config", "user.email", "integration-test@monoco.example")
	mustRun(t, wt, "git", "config", "user.name", "monoco integration test")
	mustRun(t, wt, "git", "checkout", "-b", testBranch)

	// 3. Make a feat commit on storage so there's something to propagate.
	newStorageSrc := `// Package storage is the leaf module in the test monorepo.
package storage

// Get returns a stub value.
func Get(key string) string {
	return "storage:" + key
}

// Batch is the new symbol introduced by run ` + runID + `.
func Batch(keys []string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = Get(k)
	}
	return out
}
`
	if err := os.WriteFile(filepath.Join(wt, "modules/storage/storage.go"), []byte(newStorageSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	// 4. Add a workspace-local `replace` in api/go.mod so monoco's replace-
	//    driven affected detection picks up storage as directly-affected.
	apiMod := filepath.Join(wt, "modules/api/go.mod")
	apiModBytes, err := os.ReadFile(apiMod)
	if err != nil {
		t.Fatal(err)
	}
	replaceLine := "\nreplace " + modPath + "/modules/storage => ../storage\n"
	if err := os.WriteFile(apiMod, append(apiModBytes, []byte(replaceLine)...), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, wt, "git", "add", "-A")
	mustRun(t, wt, "git", "commit", "-m", "wip(storage): add Batch + local replace, run "+runID)

	// 5. `monoco release --dry-run` — show the plan.
	planOut := mustCapture(t, wt, mono,
		"release", "--dry-run",
		"--bump", "modules/storage=minor",
		"--slug", runID,
	)
	t.Logf("release --dry-run:\n%s", planOut)
	if !strings.Contains(planOut, modPath+"/modules/storage") {
		t.Fatalf("plan missing storage entry:\n%s", planOut)
	}
	if !strings.Contains(planOut, modPath+"/modules/api") {
		t.Fatalf("plan missing cascaded api entry:\n%s", planOut)
	}
	if strings.Contains(planOut, modPath+"/modules/auth") {
		t.Fatalf("plan incorrectly includes auth:\n%s", planOut)
	}

	// 6. `monoco release -y --bump …` — push it.
	applyOut := mustCapture(t, wt, mono,
		"release", "-y",
		"--bump", "modules/storage=minor",
		"--slug", runID,
		"--remote", "origin",
	)
	t.Logf("release:\n%s", applyOut)
	if !strings.Contains(applyOut, "Pushed to origin") {
		t.Fatalf("release did not push:\n%s", applyOut)
	}

	// 7. Parse the new api version from the dry-run plan output.
	newAPIVersion := findNewVersion(t, planOut, modPath+"/modules/api")
	newStorageVersion := findNewVersion(t, planOut, modPath+"/modules/storage")
	trainTag := "train/" + todayUTC() + "-" + runID
	t.Logf("expected new versions: storage=%s api=%s train=%s",
		newStorageVersion, newAPIVersion, trainTag)

	// 8. Confirm tags landed on the remote.
	remoteRefs := mustCapture(t, wt, "git", "ls-remote", "origin",
		"refs/tags/modules/api/"+newAPIVersion,
		"refs/tags/modules/storage/"+newStorageVersion,
		"refs/tags/"+trainTag)
	t.Logf("ls-remote after apply:\n%s", remoteRefs)
	for _, want := range []string{
		"refs/tags/modules/api/" + newAPIVersion,
		"refs/tags/modules/storage/" + newStorageVersion,
		"refs/tags/" + trainTag,
	} {
		if !strings.Contains(remoteRefs, want) {
			t.Errorf("remote missing %s", want)
		}
	}

	// 9. External-consumer probe: in a fresh temp dir, create a consumer
	//    that go-gets api@<newAPIVersion>. If this works, monoco's tags
	//    are genuinely valid from an outside-the-monorepo Go toolchain POV.
	consumerDir := t.TempDir()
	consumerGoMod := "module monoco-integration-consumer\n\ngo 1.22\n"
	consumerMain := "package main\n\nimport (\n\t\"fmt\"\n\n\t\"" +
		modPath + "/modules/api\"\n)\n\nfunc main() {\n\tfmt.Println(api.Fetch(\"probe\"))\n}\n"
	if err := os.WriteFile(filepath.Join(consumerDir, "go.mod"), []byte(consumerGoMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consumerDir, "main.go"), []byte(consumerMain), 0o644); err != nil {
		t.Fatal(err)
	}

	// Force direct VCS resolution (bypass the public proxy, which may not
	// have indexed our new tags yet) and disable sumdb (our test module is
	// not in the public sumdb).
	env := append(os.Environ(),
		"GOWORK=off",
		"GOPROXY=direct",
		"GOSUMDB=off",
		"GOFLAGS=-mod=mod",
	)
	get := exec.Command("go", "get", modPath+"/modules/api@"+newAPIVersion)
	get.Dir = consumerDir
	get.Env = env
	if out, err := get.CombinedOutput(); err != nil {
		t.Fatalf("consumer `go get %s/modules/api@%s` failed: %v\n%s",
			modPath, newAPIVersion, err, out)
	}

	build := exec.Command("go", "build", "./...")
	build.Dir = consumerDir
	build.Env = env
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("consumer `go build` against new api tag failed: %v\n%s", err, out)
	}
	t.Log("consumer build succeeded — monoco-produced tags are consumable externally.")

	// 10. Leave state on remote as-is (namespaced history). Branch and tags
	//     will accumulate; old runs can be cleaned manually or via a sweep.
	t.Logf("integration run %s complete. Branch %q and its tags remain on origin.", runID, testBranch)
}

// --- helpers ---

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

func buildMonoco(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "monoco")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/matt0x6f/monoco/cmd/monoco")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build monoco: %v\n%s", err, out)
	}
	return bin
}

func runIDNow(t *testing.T) string {
	t.Helper()
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
}

func todayUTC() string {
	return time.Now().UTC().Format("2006-01-02")
}

func mustRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func mustCapture(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %v: %v\nstdout:\n%s\nstderr:\n%s",
			name, args, err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func trim(s string) string {
	return strings.TrimSpace(s)
}

// findNewVersion scans a monoco plan-table line for the given module path
// and returns the NEW version column value. Plan lines look like:
//
//	MODULE                                             OLD     NEW     KIND   DIRECT
//	github.com/matt0x6F/.../modules/storage            v0.1.0  v0.2.0  minor  direct
//	github.com/matt0x6F/.../modules/api                v0.1.0  v0.1.1  patch  cascade
func findNewVersion(t *testing.T, planOut, modulePath string) string {
	t.Helper()
	for _, line := range strings.Split(planOut, "\n") {
		if !strings.Contains(line, modulePath) {
			continue
		}
		// The line starts with the module path followed by whitespace-
		// separated OLD and NEW columns. Split on whitespace and grab
		// the third token (module, old, new).
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if fields[0] != modulePath {
			continue
		}
		return fields[2]
	}
	t.Fatalf("could not find new version for %s in plan output:\n%s", modulePath, planOut)
	return ""
}
