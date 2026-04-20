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
