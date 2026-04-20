package propagate

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/matt0x6f/monoco/internal/bump"
	"github.com/matt0x6f/monoco/internal/fixture"
	"github.com/matt0x6f/monoco/internal/workspace"
)

func TestNewPlanForModules_explicitBumpAndCascade(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	run(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")
	run(t, fx.Root, "git", "tag", "modules/api/v0.1.0")

	ws, err := workspace.Load(fx.Root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	plan, err := NewPlanForModules(ws, []string{"example.com/mono/storage"}, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/storage": bump.Minor},
	})
	if err != nil {
		t.Fatalf("NewPlanForModules: %v", err)
	}

	storage := findEntry(plan, "example.com/mono/storage")
	if storage == nil || storage.NewVersion != "v0.2.0" || !storage.DirectChange {
		t.Errorf("storage entry wrong: %+v", storage)
	}
	api := findEntry(plan, "example.com/mono/api")
	if api == nil || api.NewVersion != "v0.1.1" || api.DirectChange {
		t.Errorf("api cascade wrong (want v0.1.1, DirectChange=false): %+v", api)
	}
	if plan.TrainTag == "" {
		t.Error("TrainTag unset")
	}
}

func TestNewPlanForModules_failsClosedOnMissingBump(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	run(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")
	ws, _ := workspace.Load(fx.Root)

	_, err := NewPlanForModules(ws, []string{"example.com/mono/storage"}, Options{Slug: "test"})
	if err == nil || !strings.Contains(err.Error(), "no bump specified") {
		t.Fatalf("expected fail-closed error, got: %v", err)
	}
}

func TestNewPlanForModules_skipDropsModule(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	run(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")
	run(t, fx.Root, "git", "tag", "modules/api/v0.1.0")
	ws, _ := workspace.Load(fx.Root)

	plan, err := NewPlanForModules(ws, []string{"example.com/mono/storage"}, Options{
		Slug: "test",
		Bumps: map[string]bump.Kind{
			"example.com/mono/storage": bump.Skip,
		},
	})
	if err != nil {
		t.Fatalf("NewPlanForModules: %v", err)
	}
	for _, e := range plan.Entries {
		if e.ModulePath == "example.com/mono/storage" {
			t.Errorf("skipped storage should not appear in plan: %+v", e)
		}
	}
}

func TestNewPlanForModules_rejectsMajorBumpV1ToV2(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	run(t, fx.Root, "git", "tag", "modules/storage/v1.0.0")
	ws, _ := workspace.Load(fx.Root)

	_, err := NewPlanForModules(ws, []string{"example.com/mono/storage"}, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/storage": bump.Major},
	})
	if err == nil {
		t.Fatal("expected v1→v2 rejection")
	}
}

func TestNewPlanForModules_topoOrder(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
			{Name: "gateway", DependsOn: []string{"api"}},
		},
	})
	for _, m := range []string{"storage", "api", "gateway"} {
		run(t, fx.Root, "git", "tag", "modules/"+m+"/v0.1.0")
	}
	ws, _ := workspace.Load(fx.Root)
	plan, err := NewPlanForModules(ws, []string{"example.com/mono/storage"}, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/storage": bump.Minor},
	})
	if err != nil {
		t.Fatalf("NewPlanForModules: %v", err)
	}
	order := plan.ModulePaths()
	idx := map[string]int{}
	for i, p := range order {
		idx[p] = i
	}
	if idx["example.com/mono/storage"] >= idx["example.com/mono/api"] {
		t.Errorf("expected storage before api: %v", order)
	}
	if idx["example.com/mono/api"] >= idx["example.com/mono/gateway"] {
		t.Errorf("expected api before gateway: %v", order)
	}
}

func TestNewPlanForModules_unknownModuleError(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	ws, err := workspace.Load(fx.Root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err = NewPlanForModules(ws, []string{"example.com/does/not/exist"}, Options{Slug: "test"})
	if err == nil {
		t.Fatal("expected error for unknown module")
	}
	if !strings.Contains(err.Error(), "example.com/does/not/exist") {
		t.Errorf("error should name the bad module, got: %v", err)
	}
}

func TestResolveModuleRef_acceptsRelDirAndModulePath(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	ws, _ := workspace.Load(fx.Root)
	if p, ok := ResolveModuleRef(ws, "example.com/mono/storage"); !ok || p != "example.com/mono/storage" {
		t.Errorf("module path: got (%s, %v)", p, ok)
	}
	if p, ok := ResolveModuleRef(ws, "modules/storage"); !ok || p != "example.com/mono/storage" {
		t.Errorf("RelDir: got (%s, %v)", p, ok)
	}
	if p, ok := ResolveModuleRef(ws, "./modules/storage"); !ok || p != "example.com/mono/storage" {
		t.Errorf("dirty RelDir: got (%s, %v)", p, ok)
	}
	if _, ok := ResolveModuleRef(ws, "nope"); ok {
		t.Error("expected not-found for bogus ref")
	}
}

func findEntry(p *Plan, path string) *Entry {
	for i := range p.Entries {
		if p.Entries[i].ModulePath == path {
			return &p.Entries[i]
		}
	}
	return nil
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func headSHA(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse: %v: %s", err, out)
	}
	s := string(out)
	if n := len(s); n > 0 && s[n-1] == '\n' {
		s = s[:n-1]
	}
	return s
}
