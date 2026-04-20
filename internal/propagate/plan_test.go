package propagate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matt0x6f/monoco/internal/fixture"
	"github.com/matt0x6f/monoco/internal/workspace"
)

func TestNewPlan_affectedSetAndBumps(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
			{Name: "auth"},
		},
	})
	// Tag initial versions (steady-state).
	run(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")
	run(t, fx.Root, "git", "tag", "modules/api/v0.1.0")
	run(t, fx.Root, "git", "tag", "modules/auth/v0.1.0")

	base := headSHA(t, fx.Root)

	// feat(storage) edit.
	writeFile(t, filepath.Join(fx.Root, "modules/storage/storage.go"), "package storage\n\nfunc StorageHello() string { return \"new\" }\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "feat(storage): tweak")

	ws, err := workspace.Load(fx.Root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	plan, err := NewPlan(ws, base, "HEAD", Options{Slug: "test"})
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}

	// Expect storage (touched) and api (transitive via require) in the plan;
	// auth absent.
	paths := plan.ModulePaths()
	if !contains(paths, "example.com/mono/storage") {
		t.Errorf("plan missing storage: %v", paths)
	}
	if !contains(paths, "example.com/mono/api") {
		t.Errorf("plan missing api (transitive): %v", paths)
	}
	if contains(paths, "example.com/mono/auth") {
		t.Errorf("plan includes auth (should not): %v", paths)
	}

	// storage bumped minor (feat).
	storage := findEntry(plan, "example.com/mono/storage")
	if storage == nil || storage.OldVersion != "v0.1.0" || storage.NewVersion != "v0.2.0" {
		t.Errorf("storage entry wrong: %+v", storage)
	}
	// api had no direct commits but rides the cascade — patch bump (default for
	// consumers pulled in via transitive).
	api := findEntry(plan, "example.com/mono/api")
	if api == nil || api.OldVersion != "v0.1.0" || api.NewVersion != "v0.1.1" {
		t.Errorf("api entry wrong: %+v", api)
	}

	if plan.TrainTag == "" {
		t.Error("TrainTag unset")
	}
}

func TestNewPlan_topoOrderPutsDepsFirst(t *testing.T) {
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
	base := headSHA(t, fx.Root)
	writeFile(t, filepath.Join(fx.Root, "modules/storage/storage.go"), "package storage\n\nfunc StorageHello() string { return \"new\" }\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "feat(storage): tweak")

	ws, _ := workspace.Load(fx.Root)
	plan, err := NewPlan(ws, base, "HEAD", Options{Slug: "test"})
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}

	// Topo order: storage < api < gateway.
	order := plan.ModulePaths()
	idx := map[string]int{}
	for i, p := range order {
		idx[p] = i
	}
	if idx["example.com/mono/storage"] >= idx["example.com/mono/api"] {
		t.Errorf("expected storage before api; got order %v", order)
	}
	if idx["example.com/mono/api"] >= idx["example.com/mono/gateway"] {
		t.Errorf("expected api before gateway; got order %v", order)
	}
}

func TestNewPlan_rejectsMajorBumpV1ToV2(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	run(t, fx.Root, "git", "tag", "modules/storage/v1.0.0")
	base := headSHA(t, fx.Root)
	writeFile(t, filepath.Join(fx.Root, "modules/storage/storage.go"), "package storage\n\nfunc StorageHello() string { return \"new\" }\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "feat(storage)!: drop legacy")

	ws, _ := workspace.Load(fx.Root)
	_, err := NewPlan(ws, base, "HEAD", Options{Slug: "test"})
	if err == nil {
		t.Fatal("expected NewPlan to reject v1→v2 major bump in v1 of monoco")
	}
}

func TestNewPlanForModules_explicitListSkipsDiff(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	run(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")
	run(t, fx.Root, "git", "tag", "modules/api/v0.1.0")

	// Edit storage (should be ignored by --modules mode).
	writeFile(t, filepath.Join(fx.Root, "modules/storage/storage.go"), "package storage\n\nfunc StorageHello() string { return \"new\" }\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "feat(storage): tweak")

	ws, err := workspace.Load(fx.Root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	plan, err := NewPlanForModules(ws, []string{"example.com/mono/api"}, Options{Slug: "test"})
	if err != nil {
		t.Fatalf("NewPlanForModules: %v", err)
	}
	paths := plan.ModulePaths()
	if len(paths) != 1 || paths[0] != "example.com/mono/api" {
		t.Fatalf("expected plan with only api, got %v", paths)
	}
	api := findEntry(plan, "example.com/mono/api")
	if api == nil {
		t.Fatal("api entry missing")
	}
	if !api.DirectChange {
		t.Error("api should be DirectChange=true (explicitly listed)")
	}
	if api.OldVersion != "v0.1.0" || api.NewVersion != "v0.1.1" {
		t.Errorf("api version wrong: old=%s new=%s", api.OldVersion, api.NewVersion)
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
	plan, err := NewPlanForModules(ws, []string{
		"example.com/mono/gateway",
		"example.com/mono/storage",
		"example.com/mono/api",
	}, Options{Slug: "test"})
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

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
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
