package propagate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matt0x6f/monoco/internal/bump"
	"github.com/matt0x6f/monoco/internal/fixture"
	"github.com/matt0x6f/monoco/internal/workspace"
)

// TestApply_majorBumpRewritesPathsAndImports exercises the full
// v1→v2 rewrite path: module directive, downstream requires, .go
// import specs, tags, and the module-mode Verify step.
func TestApply_majorBumpRewritesPathsAndImports(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	run(t, fx.Root, "git", "tag", "modules/storage/v1.0.0")
	run(t, fx.Root, "git", "tag", "modules/api/v1.0.0")
	run(t, fx.Root, "git", "push", "origin", "main", "--tags")

	// Simulate an in-progress storage change so it's eligible to release.
	writeFile(t, filepath.Join(fx.Root, "modules/storage/storage.go"),
		"package storage\n\nfunc StorageHello() string { return \"v2-new\" }\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "storage: breaking change")

	ws, _ := workspace.Load(fx.Root)
	plan, err := NewPlanForModules(ws, []string{"example.com/mono/storage"}, Options{
		Slug:       "major-test",
		Bumps:      map[string]bump.Kind{"example.com/mono/storage": bump.Major},
		AllowMajor: map[string]struct{}{"example.com/mono/storage": {}},
	})
	if err != nil {
		t.Fatalf("NewPlanForModules: %v", err)
	}

	storageEntry := findEntry(plan, "example.com/mono/storage")
	if storageEntry == nil || !storageEntry.MajorBump || storageEntry.NewVersion != "v2.0.0" {
		t.Fatalf("storage entry wrong: %+v", storageEntry)
	}

	if _, err := Apply(ws, plan, ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// storage/go.mod's module directive should now carry /v2.
	storageMod, err := os.ReadFile(filepath.Join(fx.Root, "modules/storage/go.mod"))
	if err != nil {
		t.Fatalf("read storage/go.mod: %v", err)
	}
	if !strings.Contains(string(storageMod), "module example.com/mono/storage/v2") {
		t.Errorf("storage/go.mod module directive not rewritten:\n%s", storageMod)
	}

	// api/go.mod: require rewritten to /v2 at v2.0.0.
	apiMod, err := os.ReadFile(filepath.Join(fx.Root, "modules/api/go.mod"))
	if err != nil {
		t.Fatalf("read api/go.mod: %v", err)
	}
	if !strings.Contains(string(apiMod), "example.com/mono/storage/v2 v2.0.0") {
		t.Errorf("api/go.mod require not rewritten:\n%s", apiMod)
	}
	if strings.Contains(string(apiMod), "example.com/mono/storage v") && !strings.Contains(string(apiMod), "example.com/mono/storage/v2") {
		t.Errorf("api/go.mod still has old require path:\n%s", apiMod)
	}

	// api/api.go: import spec rewritten to /v2.
	apiSrc, err := os.ReadFile(filepath.Join(fx.Root, "modules/api/api.go"))
	if err != nil {
		t.Fatalf("read api/api.go: %v", err)
	}
	if !strings.Contains(string(apiSrc), `"example.com/mono/storage/v2"`) {
		t.Errorf("api/api.go import not rewritten:\n%s", apiSrc)
	}

	// Tag for the major version exists.
	if !tagExists(t, fx.Root, "modules/storage/v2.0.0") {
		t.Error("missing tag modules/storage/v2.0.0")
	}
	if !tagExists(t, fx.Root, "modules/api/v1.0.1") {
		t.Error("missing cascade tag modules/api/v1.0.1")
	}
}

// TestApply_majorBumpWithoutAllowRejects keeps the existing fail-closed
// behavior for the un-opted-in case.
func TestApply_majorBumpWithoutAllowRejects(t *testing.T) {
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
		t.Fatal("expected reject without AllowMajor")
	}
}
