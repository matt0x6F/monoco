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

// TestRewriteGoMods_SumEntriesMatchPostRewriteContent pins the ordering
// property behind every multi-level cascade: the h1: lines written into a
// consumer's go.sum must hash the dependency's content as it will exist
// at the release commit — AFTER the dependency's own go.mod/go.sum were
// rewritten. Hashing pre-rewrite state poisons the entry for every
// mid-chain module: the next module-mode build or `go mod tidy` fails
// with Go's checksum-mismatch SECURITY ERROR.
//
// Chain: core ← storage ← api. Releasing core rewrites storage's go.mod
// (require bump + replace strip) and go.sum (new core lines), so the
// storage@v0.1.1 entry in api/go.sum must match storage's post-rewrite
// directory, not its pre-rewrite one.
func TestRewriteGoMods_SumEntriesMatchPostRewriteContent(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "core"},
			{Name: "storage", DependsOn: []string{"core"}},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	run(t, fx.Root, "git", "tag", "modules/core/v0.1.0")
	run(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")
	run(t, fx.Root, "git", "tag", "modules/api/v0.1.0")

	// In-flight change to core, declared via a workspace-local replace
	// in storage (so the release also strips it from storage/go.mod).
	storageGoMod := filepath.Join(fx.Root, "modules/storage/go.mod")
	orig, err := osReadFile(storageGoMod)
	if err != nil {
		t.Fatalf("read storage go.mod: %v", err)
	}
	if err := osWriteFile(storageGoMod, orig+"\nreplace example.com/mono/core => ../core\n"); err != nil {
		t.Fatalf("write storage go.mod: %v", err)
	}
	writeFile(t, filepath.Join(fx.Root, "modules/core/core.go"),
		"package core\n\nfunc CoreHello() string { return \"core\" }\nfunc Feature() string { return \"new\" }\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "core: feature, storage: replace")

	ws, err := workspace.Load(fx.Root)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	plan, err := NewPlanForModules(ws, []string{"example.com/mono/core"}, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/core": bump.Minor},
	})
	if err != nil {
		t.Fatalf("NewPlanForModules: %v", err)
	}

	if err := rewriteGoMods(ws, plan); err != nil {
		t.Fatalf("rewriteGoMods: %v", err)
	}

	// The canonical truth: hash each dependency's directory as it now
	// exists on disk — the content the release commit will tag.
	cases := []struct {
		depPath, depDir, version, consumerSum string
	}{
		{"example.com/mono/core", "modules/core", "v0.2.0", "modules/storage/go.sum"},
		{"example.com/mono/storage", "modules/storage", "v0.1.1", "modules/api/go.sum"},
	}
	for _, c := range cases {
		want, err := ComputeModuleHashes(filepath.Join(fx.Root, c.depDir), c.depPath, c.version)
		if err != nil {
			t.Fatalf("hash post-rewrite %s: %v", c.depPath, err)
		}
		sumBytes, err := os.ReadFile(filepath.Join(fx.Root, c.consumerSum))
		if err != nil {
			t.Fatalf("read %s: %v", c.consumerSum, err)
		}
		sum := string(sumBytes)
		zipLine := c.depPath + " " + c.version + " " + want.H1
		modLine := c.depPath + " " + c.version + "/go.mod " + want.H1Mod
		if !strings.Contains(sum, zipLine) {
			t.Errorf("%s pins a hash for %s@%s that does not match the post-rewrite content.\nwant line: %s\ngot:\n%s",
				c.consumerSum, c.depPath, c.version, zipLine, sum)
		}
		if !strings.Contains(sum, modLine) {
			t.Errorf("%s pins a go.mod hash for %s@%s that does not match the post-rewrite go.mod.\nwant line: %s\ngot:\n%s",
				c.consumerSum, c.depPath, c.version, modLine, sum)
		}
	}
}
