package propagate

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/matt0x6f/monoco/internal/bump"
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

	writeFile(t, filepath.Join(fx.Root, "modules/storage/storage.go"),
		"package storage\n\nfunc StorageHello() string { return \"new\" }\nfunc Batch() string { return \"batch\" }\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "storage: add Batch")

	ws, err := workspace.Load(fx.Root)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	plan, err := NewPlanForModules(ws, []string{"example.com/mono/storage"}, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/storage": bump.Minor},
	})
	if err != nil {
		t.Fatalf("NewPlanForModules: %v", err)
	}

	statusBefore := gitStatus(t, fx.Root)

	rewrites, err := ComputeRewrites(ws, plan)
	if err != nil {
		t.Fatalf("ComputeRewrites: %v", err)
	}

	if got := gitStatus(t, fx.Root); got != statusBefore {
		t.Errorf("ComputeRewrites mutated working tree.\nbefore:\n%s\nafter:\n%s", statusBefore, got)
	}

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
	if strings.Contains(string(apiRewrite.Old), "example.com/mono/storage v0.2.0") {
		t.Errorf("Old should not already contain the new version:\n%s", apiRewrite.Old)
	}

	// go.sum additions should include both h1: lines for storage@v0.2.0.
	joined := strings.Join(apiRewrite.SumAdds, "\n")
	if !strings.Contains(joined, "example.com/mono/storage v0.2.0 h1:") {
		t.Errorf("SumAdds missing storage content h1 line: %v", apiRewrite.SumAdds)
	}
	if !strings.Contains(joined, "example.com/mono/storage v0.2.0/go.mod h1:") {
		t.Errorf("SumAdds missing storage go.mod h1 line: %v", apiRewrite.SumAdds)
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

func TestComputeRewrites_stripsWorkspaceLocalReplaces(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	run(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")
	run(t, fx.Root, "git", "tag", "modules/api/v0.1.0")

	// Inject a workspace-local replace in api/go.mod.
	apiGoMod := filepath.Join(fx.Root, "modules/api/go.mod")
	orig, err := osReadFile(apiGoMod)
	if err != nil {
		t.Fatalf("read api go.mod: %v", err)
	}
	if err := osWriteFile(apiGoMod, orig+"\nreplace example.com/mono/storage => ../storage\n"); err != nil {
		t.Fatalf("write api go.mod: %v", err)
	}
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "api: replace storage -> local")

	ws, _ := workspace.Load(fx.Root)
	plan, err := NewPlanForModules(ws, []string{"example.com/mono/storage"}, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/storage": bump.Minor},
	})
	if err != nil {
		t.Fatalf("NewPlanForModules: %v", err)
	}
	rewrites, err := ComputeRewrites(ws, plan)
	if err != nil {
		t.Fatalf("ComputeRewrites: %v", err)
	}
	apiRewrite := rewrites["example.com/mono/api"]
	if strings.Contains(string(apiRewrite.New), "replace example.com/mono/storage") {
		t.Errorf("replace directive not stripped:\n%s", apiRewrite.New)
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
