package affected

import (
	"sort"
	"testing"

	"github.com/matt0x6f/monoco/internal/fixture"
	"github.com/matt0x6f/monoco/internal/workspace"
)

func TestCompute_transitivelyPullsInConsumers(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
			{Name: "gateway", DependsOn: []string{"api"}},
			{Name: "auth"},
		},
	})
	ws, err := workspace.Load(fx.Root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := Compute(ws, []string{"example.com/mono/storage"})
	sort.Strings(got)
	want := []string{
		"example.com/mono/api",
		"example.com/mono/gateway",
		"example.com/mono/storage",
	}
	if !equal(got, want) {
		t.Errorf("Compute(storage) = %v; want %v", got, want)
	}

	got = Compute(ws, []string{"example.com/mono/auth"})
	sort.Strings(got)
	if want := []string{"example.com/mono/auth"}; !equal(got, want) {
		t.Errorf("Compute(auth) = %v; want %v", got, want)
	}
}

func TestFromTouchedFiles_mapsPathsToModules(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	ws, err := workspace.Load(fx.Root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	modules := FromTouchedFiles(ws, []string{
		"modules/storage/storage.go",
		"README.md", // outside any module — ignored
	})
	sort.Strings(modules)
	if want := []string{"example.com/mono/storage"}; !equal(modules, want) {
		t.Errorf("FromTouchedFiles = %v; want %v", modules, want)
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
