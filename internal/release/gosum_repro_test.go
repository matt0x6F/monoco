package release

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matt0x6f/monoco/internal/bump"
	"github.com/matt0x6f/monoco/internal/fixture"
	"github.com/matt0x6f/monoco/internal/workspace"
)

// TestRelease_WithWorkspaceReplace_WritesGoSum reproduces the path
// exercised by the integration suite: direct-affected detection via a
// workspace-local `replace` directive, followed by release.Plan +
// release.Apply. Asserts the downstream go.sum is populated — the
// property that, in a run against matt0x6F/monoco-test-monorepo on
// 2026-04-19, silently failed to hold.
func TestRelease_WithWorkspaceReplace_WritesGoSum(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	mustGit(t, fx.Root, "tag", "modules/storage/v0.1.0")
	mustGit(t, fx.Root, "tag", "modules/api/v0.1.0")
	mustGit(t, fx.Root, "push", "origin", "main", "--tags")

	// Simulate `addLocalReplace`: append a workspace-local replace to
	// api/go.mod and commit. This is what monoco's release flow looks
	// for when identifying direct-affected modules.
	apiModPath := filepath.Join(fx.Root, "modules/api/go.mod")
	orig, err := os.ReadFile(apiModPath)
	if err != nil {
		t.Fatal(err)
	}
	withReplace := append(orig,
		[]byte("\nreplace example.com/mono/storage => ../storage\n")...)
	if err := os.WriteFile(apiModPath, withReplace, 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, fx.Root, "add", "-A")
	mustGit(t, fx.Root, "commit", "-m", "local: replace storage for dev")

	// Edit storage so there's a real change to ship.
	storageSrc := filepath.Join(fx.Root, "modules/storage/storage.go")
	if err := os.WriteFile(storageSrc,
		[]byte("package storage\n\nfunc StorageHello() string { return \"new\" }\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, fx.Root, "add", "-A")
	mustGit(t, fx.Root, "commit", "-m", "storage: change")

	// Run the full release path: Plan + Apply, like the CLI does.
	ws, err := workspace.Load(fx.Root)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	plan, err := Plan(ws, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/storage": bump.Minor},
	}, &out)
	if err != nil {
		t.Fatalf("Plan: %v\n%s", err, out.String())
	}
	if plan == nil {
		t.Fatalf("plan was nil; stdout:\n%s", out.String())
	}
	if _, err := Apply(ws, plan, Options{Remote: "origin"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// The bug: the published release commit on the real test monorepo
	// contains no go.sum. Assert that this fixture now does.
	apiSumPath := filepath.Join(fx.Root, "modules/api/go.sum")
	sum, err := os.ReadFile(apiSumPath)
	if err != nil {
		t.Fatalf("api/go.sum missing after release: %v\n\nplan stdout:\n%s", err, out.String())
	}
	s := string(sum)
	if !strings.Contains(s, "example.com/mono/storage v0.2.0 h1:") {
		t.Errorf("api/go.sum missing storage h1 zip line:\n%s", s)
	}
	if !strings.Contains(s, "example.com/mono/storage v0.2.0/go.mod h1:") {
		t.Errorf("api/go.sum missing storage go.mod h1 line:\n%s", s)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
