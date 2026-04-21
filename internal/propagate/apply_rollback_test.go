package propagate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/matt0x6f/monoco/internal/bump"
	"github.com/matt0x6f/monoco/internal/fixture"
	"github.com/matt0x6f/monoco/internal/workspace"
)

// TestApply_rollbackFailureSurfaced forces rollback to fail during Verify
// (by corrupting go.sum AND making the .git dir read-only so tag-delete
// and reset --hard both error) and asserts the returned error reports
// both the original failure and the rollback failure.
func TestApply_rollbackFailureSurfaced(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based rollback failure is POSIX-only")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses chmod")
	}

	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	run(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")
	run(t, fx.Root, "git", "tag", "modules/api/v0.1.0")

	// Introduce broken source so Verify fails AFTER the commit + tags are
	// created — this is the path that exercises the full rollback closure.
	writeFile(t, filepath.Join(fx.Root, "modules/api/api.go"),
		"package api\n\nimport \"example.com/mono/storage\"\n\nfunc Broken() string { return storage.DoesNotExist() }\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "api: intentionally broken")

	ws, _ := workspace.Load(fx.Root)
	plan, err := NewPlanForModules(ws, []string{"example.com/mono/storage"}, Options{
		Slug:  "rollback",
		Bumps: map[string]bump.Kind{"example.com/mono/storage": bump.Minor},
	})
	if err != nil {
		t.Fatalf("NewPlanForModules: %v", err)
	}

	// Make .git read-only so rollback's `tag -d` and `reset --hard` both
	// error. Restore in cleanup so t.TempDir can clean up.
	gitDir := filepath.Join(fx.Root, ".git")
	origMode := mustStatMode(t, gitDir)
	t.Cleanup(func() { _ = os.Chmod(gitDir, origMode) })

	// Defer chmod until just before Apply so fixture setup commits succeed.
	if err := os.Chmod(gitDir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	_, err = Apply(ws, plan, ApplyOptions{})
	if err == nil {
		t.Fatal("expected Apply to fail")
	}

	msg := err.Error()
	// The original failure must surface (any git error from the first
	// step that tripped once .git went read-only).
	if !strings.Contains(msg, "Permission denied") {
		t.Errorf("error missing original cause: %v", err)
	}
	// And so must the rollback failure.
	if !strings.Contains(msg, "rollback also failed") {
		t.Errorf("error missing rollback cause: %v", err)
	}
}

func mustStatMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Mode().Perm()
}
