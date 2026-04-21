package propagate

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	if err := Verify(context.Background(), ws, []string{"example.com/mono/api"}); err != nil {
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
	err := Verify(context.Background(), ws, []string{"example.com/mono/api"})
	if err == nil {
		t.Fatal("Verify succeeded on broken source")
	}
}

// TestVerify_parallelAbortsOnFirstFailure builds a 20-module fixture with
// one module (m7) containing a deliberate source error. Verify must return
// an error that names m7, and the log line captures wall time + effective
// throughput per issue #4's acceptance criteria.
func TestVerify_parallelAbortsOnFirstFailure(t *testing.T) {
	const n = 20
	const broken = 7
	specs := make([]fixture.ModuleSpec, n)
	paths := make([]string, n)
	for i := range specs {
		specs[i] = fixture.ModuleSpec{Name: fmt.Sprintf("m%d", i)}
		paths[i] = fmt.Sprintf("example.com/mono/m%d", i)
	}
	fx := fixture.New(t, fixture.Spec{Modules: specs})

	// Break m<broken> by calling an undefined symbol.
	brokenFile := filepath.Join(fx.Root, fmt.Sprintf("modules/m%d/m%d.go", broken, broken))
	writeFile(t, brokenFile,
		fmt.Sprintf("package m%d\n\nfunc M%dHello() string {\n\treturn doesNotExist()\n}\n", broken, broken))
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "break m7")

	ws, err := workspace.Load(fx.Root)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}

	start := time.Now()
	err = Verify(context.Background(), ws, paths)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Verify succeeded on fixture with one broken module")
	}
	brokenMarker := fmt.Sprintf("modules/m%d", broken)
	if !strings.Contains(err.Error(), brokenMarker) {
		t.Fatalf("error does not name the broken module %q: %v", brokenMarker, err)
	}

	modulesPerSec := float64(n) / elapsed.Seconds()
	t.Logf("parallel Verify: %d modules, %s elapsed, %.2f modules/s (1 deliberately broken)",
		n, elapsed, modulesPerSec)
	if modulesPerSec < 1.0 {
		t.Errorf("effective throughput %.2f modules/s below 1 modules/s threshold", modulesPerSec)
	}
}
