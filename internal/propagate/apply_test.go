package propagate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	// Initial tags so we have a clean steady state.
	run(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")
	run(t, fx.Root, "git", "tag", "modules/api/v0.1.0")
	run(t, fx.Root, "git", "push", "origin", "main", "--tags")

	base := headSHA(t, fx.Root)
	// feat(storage) change.
	writeFile(t, filepath.Join(fx.Root, "modules/storage/storage.go"),
		"package storage\n\nfunc StorageHello() string { return \"new\" }\nfunc Batch() string { return \"batch\" }\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "feat(storage): add batch")

	ws, _ := workspace.Load(fx.Root)
	plan, err := NewPlan(ws, base, "HEAD", Options{Slug: "test"})
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}

	res, err := Apply(ws, plan, ApplyOptions{Remote: "origin"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Exactly ONE release commit added on main.
	if got := countCommitsSince(t, fx.Root, base); got != 2 {
		// base + feat commit + release commit = 2 since base.
		t.Errorf("expected 2 commits after base (feat + release); got %d", got)
	}

	// api/go.mod now requires storage@v0.2.0.
	apiMod, _ := os.ReadFile(filepath.Join(fx.Root, "modules/api/go.mod"))
	if !strings.Contains(string(apiMod), "example.com/mono/storage v0.2.0") {
		t.Errorf("api/go.mod not rewritten:\n%s", apiMod)
	}

	// Tags locally: storage/v0.2.0, api/v0.1.1, train/...
	for _, expected := range []string{
		"modules/storage/v0.2.0",
		"modules/api/v0.1.1",
		plan.TrainTag,
	} {
		if !tagExists(t, fx.Root, expected) {
			t.Errorf("missing local tag %s", expected)
		}
	}

	// All tags point at the SAME commit (the release commit).
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

	// Tags on remote (atomic push succeeded).
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
	base := headSHA(t, fx.Root)

	// Break api: call nonexistent symbol on storage.
	writeFile(t, filepath.Join(fx.Root, "modules/api/api.go"),
		"package api\n\nimport \"example.com/mono/storage\"\n\nfunc ApiHello() string {\n\treturn storage.DoesNotExist()\n}\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "fix(api): use new api")

	ws, _ := workspace.Load(fx.Root)
	plan, _ := NewPlan(ws, base, "HEAD", Options{Slug: "broken"})

	_, err := Apply(ws, plan, ApplyOptions{Remote: "origin"})
	if err == nil {
		t.Fatal("Apply should have failed on verify")
	}

	// HEAD should be back at the pre-Apply commit (the broken fix(api) one).
	// No release commit, no tags.
	if tagExists(t, fx.Root, plan.TrainTag) {
		t.Error("train tag exists despite verify failure")
	}
	if tagExists(t, fx.Root, "modules/storage/v0.2.0") {
		t.Error("storage tag exists despite verify failure")
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
