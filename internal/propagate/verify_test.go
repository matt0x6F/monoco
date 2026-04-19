package propagate

import (
	"path/filepath"
	"testing"

	"github.com/matt0x6f/monoco/internal/fixture"
	"github.com/matt0x6f/monoco/internal/workspace"
)

func TestVerify_consistentRewrite(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	// Rewrite api/go.mod to require a new storage version. Source still compiles.
	rewriteRequire(t, filepath.Join(fx.Root, "modules/api/go.mod"),
		"example.com/mono/storage", "v0.9.0")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "release candidate")

	ws, _ := workspace.Load(fx.Root)

	if err := Verify(ws, []string{"example.com/mono/api"}); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// Working tree clean (no leftover go.verify.mod).
	status := gitStatus(t, fx.Root)
	if status != "" {
		t.Errorf("working tree not clean after Verify:\n%s", status)
	}
}

func TestVerify_catchesBrokenSource(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	// Break api: call a symbol storage doesn't export.
	writeFile(t, filepath.Join(fx.Root, "modules/api/api.go"),
		"package api\n\nimport \"example.com/mono/storage\"\n\nfunc ApiHello() string {\n\treturn storage.DoesNotExist()\n}\n")
	rewriteRequire(t, filepath.Join(fx.Root, "modules/api/go.mod"),
		"example.com/mono/storage", "v0.9.0")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "broken rc")

	ws, _ := workspace.Load(fx.Root)
	err := Verify(ws, []string{"example.com/mono/api"})
	if err == nil {
		t.Fatal("Verify succeeded on broken source")
	}
}
