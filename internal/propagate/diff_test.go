package propagate

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/matt0x6f/monoco/internal/fixture"
	"github.com/matt0x6f/monoco/internal/workspace"
)

func TestComputeRewrites_andUnifiedDiff(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	run(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")
	run(t, fx.Root, "git", "tag", "modules/api/v0.1.0")

	base := headSHA(t, fx.Root)
	writeFile(t, filepath.Join(fx.Root, "modules/storage/storage.go"),
		"package storage\n\nfunc StorageHello() string { return \"new\" }\nfunc Batch() string { return \"batch\" }\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "feat(storage): add batch")

	ws, err := workspace.Load(fx.Root)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	plan, err := NewPlan(ws, base, "HEAD", Options{Slug: "test"})
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}

	statusBefore := gitStatus(t, fx.Root)

	rewrites, err := ComputeRewrites(ws, plan)
	if err != nil {
		t.Fatalf("ComputeRewrites: %v", err)
	}

	// Working tree must be untouched — ComputeRewrites is read-only.
	if got := gitStatus(t, fx.Root); got != statusBefore {
		t.Errorf("ComputeRewrites mutated working tree.\nbefore:\n%s\nafter:\n%s", statusBefore, got)
	}

	// storage is a leaf direct bump — no in-plan require to update, so it's omitted.
	if _, ok := rewrites["example.com/mono/storage"]; ok {
		t.Errorf("storage has no in-plan require rewrite; should be omitted from rewrites map")
	}
	apiRewrite, ok := rewrites["example.com/mono/api"]
	if !ok {
		t.Fatalf("expected rewrite for api; got keys %v", keys(rewrites))
	}
	if !strings.Contains(string(apiRewrite.New), "example.com/mono/storage v0.2.0") {
		t.Errorf("rewrite did not bump storage to v0.2.0:\n%s", apiRewrite.New)
	}
	// Old should be the actual bytes on disk before any rewrite — the fixture
	// uses the modfile placeholder version, which the test deliberately does
	// not assume; we just assert it does NOT already contain v0.2.0.
	if strings.Contains(string(apiRewrite.Old), "example.com/mono/storage v0.2.0") {
		t.Errorf("Old should not already contain the new version:\n%s", apiRewrite.Old)
	}

	diff := UnifiedDiff("modules/api/go.mod", apiRewrite.Old, apiRewrite.New)
	for _, want := range []string{
		"--- modules/api/go.mod",
		"+++ modules/api/go.mod (proposed)",
		"-require example.com/mono/storage ",
		"+require example.com/mono/storage v0.2.0",
	} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff missing %q.\nfull diff:\n%s", want, diff)
		}
	}
}

func TestUnifiedDiff_identicalReturnsEmpty(t *testing.T) {
	got := UnifiedDiff("foo.txt", []byte("hello\n"), []byte("hello\n"))
	if got != "" {
		t.Errorf("expected empty diff for identical input; got %q", got)
	}
}

func keys(m map[string]RewrittenMod) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
