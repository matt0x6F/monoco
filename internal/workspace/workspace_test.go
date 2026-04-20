package workspace

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/matt0x6f/monoco/internal/fixture"
)

func TestLoad_graphIncludesWorkspaceEdges(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
			{Name: "auth"},
		},
	})

	ws, err := Load(fx.Root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var paths []string
	for p := range ws.Modules {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	want := []string{"example.com/mono/api", "example.com/mono/auth", "example.com/mono/storage"}
	if !equal(paths, want) {
		t.Errorf("Modules = %v; want %v", paths, want)
	}

	// api requires storage; auth requires nothing in the workspace.
	if consumers := ws.Consumers("example.com/mono/storage"); len(consumers) != 1 || consumers[0] != "example.com/mono/api" {
		t.Errorf("Consumers(storage) = %v; want [api]", consumers)
	}
	if consumers := ws.Consumers("example.com/mono/auth"); len(consumers) != 0 {
		t.Errorf("Consumers(auth) = %v; want []", consumers)
	}
}

func TestLoad_moduleDirResolvable(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	ws, err := Load(fx.Root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	m, ok := ws.Modules["example.com/mono/storage"]
	if !ok {
		t.Fatal("storage module missing from workspace")
	}
	if m.Dir == "" {
		t.Fatal("Module.Dir empty")
	}
	if m.RelDir == "" {
		t.Fatal("Module.RelDir empty")
	}
}

func TestLoad_honorsMonocoYamlExcludes(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
			{Name: "private", DependsOn: []string{"storage"}},
		},
	})

	// Drop a monoco.yaml that excludes modules/private.
	manifest := "version: 1\nexclude:\n  - modules/private\n"
	if err := os.WriteFile(filepath.Join(fx.Root, "monoco.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	ws, err := Load(fx.Root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if _, ok := ws.Modules["example.com/mono/private"]; ok {
		t.Errorf("excluded module example.com/mono/private still present in workspace")
	}
	if _, ok := ws.Modules["example.com/mono/api"]; !ok {
		t.Errorf("non-excluded module example.com/mono/api missing")
	}
	// The excluded consumer shouldn't show up as a reverse-dep of storage.
	consumers := ws.Consumers("example.com/mono/storage")
	for _, c := range consumers {
		if c == "example.com/mono/private" {
			t.Errorf("excluded module still in reverse-dep edges: %v", consumers)
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
