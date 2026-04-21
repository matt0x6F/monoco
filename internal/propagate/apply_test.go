package propagate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matt0x6f/monoco/internal/bump"
	"github.com/matt0x6f/monoco/internal/fixture"
	"github.com/matt0x6f/monoco/internal/workspace"
)

func TestApply_cascadePropagation(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	run(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")
	run(t, fx.Root, "git", "tag", "modules/api/v0.1.0")
	run(t, fx.Root, "git", "push", "origin", "main", "--tags")

	base := headSHA(t, fx.Root)

	// Apply a storage change and commit it (simulates in-progress dev).
	writeFile(t, filepath.Join(fx.Root, "modules/storage/storage.go"),
		"package storage\n\nfunc StorageHello() string { return \"new\" }\nfunc Batch() string { return \"batch\" }\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "storage: add Batch")

	ws, _ := workspace.Load(fx.Root)
	plan, err := NewPlanForModules(ws, []string{"example.com/mono/storage"}, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/storage": bump.Minor},
	})
	if err != nil {
		t.Fatalf("NewPlanForModules: %v", err)
	}

	res, err := Apply(ws, plan, ApplyOptions{Remote: "origin"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if got := countCommitsSince(t, fx.Root, base); got != 2 {
		t.Errorf("expected 2 commits after base (feat + release); got %d", got)
	}

	apiMod, _ := os.ReadFile(filepath.Join(fx.Root, "modules/api/go.mod"))
	if !strings.Contains(string(apiMod), "example.com/mono/storage v0.2.0") {
		t.Errorf("api/go.mod not rewritten:\n%s", apiMod)
	}

	// api/go.sum must now have lines for storage@v0.2.0.
	apiSum, err := os.ReadFile(filepath.Join(fx.Root, "modules/api/go.sum"))
	if err != nil {
		t.Fatalf("read api/go.sum: %v", err)
	}
	if !strings.Contains(string(apiSum), "example.com/mono/storage v0.2.0 h1:") {
		t.Errorf("api/go.sum missing storage h1 line:\n%s", apiSum)
	}
	if !strings.Contains(string(apiSum), "example.com/mono/storage v0.2.0/go.mod h1:") {
		t.Errorf("api/go.sum missing storage go.mod h1 line:\n%s", apiSum)
	}

	for _, expected := range []string{
		"modules/storage/v0.2.0",
		"modules/api/v0.1.1",
		plan.TrainTag,
	} {
		if !tagExists(t, fx.Root, expected) {
			t.Errorf("missing local tag %s", expected)
		}
	}

	releaseSHA := res.ReleaseCommit
	for _, tg := range []string{
		"modules/storage/v0.2.0",
		"modules/api/v0.1.1",
		plan.TrainTag,
	} {
		if got := tagSHA(t, fx.Root, tg); got != releaseSHA {
			t.Errorf("tag %s points at %s, not release commit %s", tg, got, releaseSHA)
		}
	}

	remoteTags := remoteTagList(t, fx.RemoteDir)
	for _, expected := range []string{
		"modules/storage/v0.2.0",
		"modules/api/v0.1.1",
		plan.TrainTag,
	} {
		if !sliceContains(remoteTags, expected) {
			t.Errorf("remote missing %s; has %v", expected, remoteTags)
		}
	}
}

func TestApply_rollsBackOnVerifyFailure(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	run(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")
	run(t, fx.Root, "git", "tag", "modules/api/v0.1.0")
	run(t, fx.Root, "git", "push", "origin", "main", "--tags")

	// Break api: call nonexistent storage symbol.
	writeFile(t, filepath.Join(fx.Root, "modules/api/api.go"),
		"package api\n\nimport \"example.com/mono/storage\"\n\nfunc ApiHello() string {\n\treturn storage.DoesNotExist()\n}\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "api: use doesnotexist")

	ws, _ := workspace.Load(fx.Root)
	plan, _ := NewPlanForModules(ws, []string{"example.com/mono/api"}, Options{
		Slug:  "broken",
		Bumps: map[string]bump.Kind{"example.com/mono/api": bump.Patch},
	})

	_, err := Apply(ws, plan, ApplyOptions{Remote: "origin"})
	if err == nil {
		t.Fatal("Apply should have failed on verify")
	}

	if tagExists(t, fx.Root, plan.TrainTag) {
		t.Error("train tag exists despite verify failure")
	}
	if tagExists(t, fx.Root, "modules/api/v0.1.1") {
		t.Error("api tag exists despite verify failure")
	}
}

func TestApply_bootstrapFromPlaceholders(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	// No pre-seeded tags: this is the first-ever propagation.
	run(t, fx.Root, "git", "push", "origin", "main")

	const placeholder = "v0.0.0-00010101000000-000000000000"
	apiModPath := filepath.Join(fx.Root, "modules/api/go.mod")
	preApiMod, err := os.ReadFile(apiModPath)
	if err != nil {
		t.Fatalf("read api/go.mod: %v", err)
	}
	if !strings.Contains(string(preApiMod), placeholder) {
		t.Fatalf("fixture should seed placeholder pseudo-version; api/go.mod:\n%s", preApiMod)
	}

	base := headSHA(t, fx.Root)

	writeFile(t, filepath.Join(fx.Root, "modules/storage/storage.go"),
		"package storage\n\nfunc StorageHello() string { return \"new\" }\nfunc Batch() string { return \"batch\" }\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "storage: add Batch")

	ws, _ := workspace.Load(fx.Root)
	plan, err := NewPlanForModules(ws, []string{"example.com/mono/storage"}, Options{
		Slug:  "bootstrap",
		Bumps: map[string]bump.Kind{"example.com/mono/storage": bump.Minor},
	})
	if err != nil {
		t.Fatalf("NewPlanForModules: %v", err)
	}

	res, err := Apply(ws, plan, ApplyOptions{Remote: "origin"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if got := countCommitsSince(t, fx.Root, base); got != 2 {
		t.Errorf("expected 2 commits after base (feat + release); got %d", got)
	}

	apiMod, _ := os.ReadFile(apiModPath)
	if strings.Contains(string(apiMod), placeholder) {
		t.Errorf("api/go.mod still contains placeholder after apply:\n%s", apiMod)
	}
	if !strings.Contains(string(apiMod), "example.com/mono/storage v0.1.0") {
		t.Errorf("api/go.mod not rewritten to storage v0.1.0:\n%s", apiMod)
	}

	apiSum, err := os.ReadFile(filepath.Join(fx.Root, "modules/api/go.sum"))
	if err != nil {
		t.Fatalf("read api/go.sum: %v", err)
	}
	if !strings.Contains(string(apiSum), "example.com/mono/storage v0.1.0 h1:") {
		t.Errorf("api/go.sum missing storage h1 line:\n%s", apiSum)
	}
	if !strings.Contains(string(apiSum), "example.com/mono/storage v0.1.0/go.mod h1:") {
		t.Errorf("api/go.sum missing storage go.mod h1 line:\n%s", apiSum)
	}

	for _, expected := range []string{
		"modules/storage/v0.1.0",
		"modules/api/v0.1.0",
		plan.TrainTag,
	} {
		if !tagExists(t, fx.Root, expected) {
			t.Errorf("missing local tag %s", expected)
		}
	}

	releaseSHA := res.ReleaseCommit
	for _, tg := range []string{
		"modules/storage/v0.1.0",
		"modules/api/v0.1.0",
		plan.TrainTag,
	} {
		if got := tagSHA(t, fx.Root, tg); got != releaseSHA {
			t.Errorf("tag %s points at %s, not release commit %s", tg, got, releaseSHA)
		}
	}

	remoteTags := remoteTagList(t, fx.RemoteDir)
	for _, expected := range []string{
		"modules/storage/v0.1.0",
		"modules/api/v0.1.0",
		plan.TrainTag,
	} {
		if !sliceContains(remoteTags, expected) {
			t.Errorf("remote missing %s; has %v", expected, remoteTags)
		}
	}
}

// TestApply_loneModuleSkipsReleaseCommit covers the bootstrap case
// where a module has no in-tree consumers: there are no go.mod
// rewrites to stage, so no release commit is created. The module tag
// and train tag must land on the existing HEAD.
func TestApply_loneModuleSkipsReleaseCommit(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	run(t, fx.Root, "git", "push", "origin", "main")

	headBefore := headSHA(t, fx.Root)

	ws, _ := workspace.Load(fx.Root)
	plan, err := NewPlanForModules(ws, []string{"example.com/mono/storage"}, Options{
		Slug:  "lone",
		Bumps: map[string]bump.Kind{"example.com/mono/storage": bump.Minor},
	})
	if err != nil {
		t.Fatalf("NewPlanForModules: %v", err)
	}

	res, err := Apply(ws, plan, ApplyOptions{Remote: "origin"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	headAfter := headSHA(t, fx.Root)
	if headAfter != headBefore {
		t.Errorf("HEAD advanced (%s -> %s); expected no release commit for lone module",
			headBefore, headAfter)
	}
	if res.ReleaseCommit != headBefore {
		t.Errorf("ReleaseCommit = %s; want pre-apply HEAD %s", res.ReleaseCommit, headBefore)
	}

	for _, tg := range []string{"modules/storage/v0.1.0", plan.TrainTag} {
		if !tagExists(t, fx.Root, tg) {
			t.Errorf("missing local tag %s", tg)
			continue
		}
		if got := tagSHA(t, fx.Root, tg); got != headBefore {
			t.Errorf("tag %s points at %s, not HEAD %s", tg, got, headBefore)
		}
	}

	remoteTags := remoteTagList(t, fx.RemoteDir)
	for _, tg := range []string{"modules/storage/v0.1.0", plan.TrainTag} {
		if !sliceContains(remoteTags, tg) {
			t.Errorf("remote missing %s; has %v", tg, remoteTags)
		}
	}
}

func TestApply_ConcurrentBaseMove(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	run(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")
	run(t, fx.Root, "git", "tag", "modules/api/v0.1.0")
	run(t, fx.Root, "git", "push", "origin", "main", "--tags")

	// Apply a storage change (simulates in-progress dev).
	writeFile(t, filepath.Join(fx.Root, "modules/storage/storage.go"),
		"package storage\n\nfunc StorageHello() string { return \"new\" }\nfunc Batch() string { return \"batch\" }\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "storage: add Batch")

	// Capture the remote base SHA exactly as release.Plan would.
	baseSHA, err := GetRemoteRefSHA(fx.Root, "origin", "refs/heads/main")
	if err != nil {
		t.Fatalf("GetRemoteRefSHA: %v", err)
	}

	ws, _ := workspace.Load(fx.Root)
	plan, err := NewPlanForModules(ws, []string{"example.com/mono/storage"}, Options{
		Slug:    "race",
		Bumps:   map[string]bump.Kind{"example.com/mono/storage": bump.Minor},
		BaseRef: "refs/heads/main",
		BaseSHA: baseSHA,
	})
	if err != nil {
		t.Fatalf("NewPlanForModules: %v", err)
	}

	// Simulate a competing run: clone the bare remote, push an
	// unrelated commit, which advances origin/main between plan and
	// apply.
	advanceRemoteMain(t, fx.RemoteDir)

	_, err = Apply(ws, plan, ApplyOptions{Remote: "origin"})
	if err == nil {
		t.Fatal("Apply should have failed due to base SHA drift")
	}
	if !strings.Contains(err.Error(), "base moved") {
		t.Fatalf("expected base-moved error, got: %v", err)
	}

	// No local tags should have been created for this run.
	for _, tg := range []string{
		"modules/storage/v0.2.0",
		"modules/api/v0.1.1",
		plan.TrainTag,
	} {
		if tagExists(t, fx.Root, tg) {
			t.Errorf("tag %s should not exist after aborted apply", tg)
		}
	}
	// No release commit either.
	if n := countCommitsSince(t, fx.Root, baseSHA); n != 1 {
		t.Errorf("expected 1 commit since base (the storage feat), got %d", n)
	}
}

// advanceRemoteMain pushes an unrelated commit to the bare remote's
// main branch from a fresh clone.
func advanceRemoteMain(t *testing.T, remoteDir string) {
	t.Helper()
	other := t.TempDir()
	run(t, other, "git", "clone", remoteDir, ".")
	// Ensure a local `main` tracking origin/main exists regardless of
	// the clone's HEAD handling across git versions.
	run(t, other, "git", "checkout", "-B", "main", "origin/main")
	run(t, other, "git", "config", "user.email", "concurrent@example.com")
	run(t, other, "git", "config", "user.name", "Concurrent")
	writeFile(t, filepath.Join(other, "concurrent.txt"), "raced\n")
	run(t, other, "git", "add", "-A")
	run(t, other, "git", "commit", "-m", "concurrent: race")
	run(t, other, "git", "push", "origin", "main")
}

func TestApply_refusesDirtyWorkingTree(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	run(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")

	// Leave an untracked file in the working tree.
	writeFile(t, filepath.Join(fx.Root, "uncommitted.txt"), "dirty\n")

	ws, _ := workspace.Load(fx.Root)
	plan, _ := NewPlanForModules(ws, []string{"example.com/mono/storage"}, Options{
		Slug:  "dirty",
		Bumps: map[string]bump.Kind{"example.com/mono/storage": bump.Patch},
	})

	_, err := Apply(ws, plan, ApplyOptions{Remote: ""})
	if err == nil || !strings.Contains(err.Error(), "not clean") {
		t.Fatalf("expected not-clean error, got: %v", err)
	}
}

func tagSHA(t *testing.T, root, tag string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "rev-list", "-n", "1", tag)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-list %s: %v: %s", tag, err, out)
	}
	return strings.TrimSpace(string(out))
}

func tagExists(t *testing.T, root, tag string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "tag", "-l", tag)
	out, _ := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)) == tag
}

func countCommitsSince(t *testing.T, root, since string) int {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "rev-list", "--count", since+"..HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-list: %v: %s", err, out)
	}
	var n int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n)
	return n
}

func remoteTagList(t *testing.T, remoteDir string) []string {
	t.Helper()
	cmd := exec.Command("git", "-C", remoteDir, "tag")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("remote tag: %v: %s", err, out)
	}
	var tags []string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			tags = append(tags, l)
		}
	}
	return tags
}

func sliceContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
