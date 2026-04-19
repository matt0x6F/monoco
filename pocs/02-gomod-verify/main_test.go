package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matt0x6f/monoco/internal/fixture"
)

func TestVerify_consistent(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})

	// Rewrite api/go.mod to require storage@v0.9.0 (simulating a propagation).
	rewriteRequire(t, filepath.Join(fx.Root, "modules/api/go.mod"),
		"example.com/mono/storage", "v0.9.0")
	commitAll(t, fx.Root, "release: train/test")

	snapshot := gitStatus(t, fx.Root)

	if err := Verify(fx.Root, []string{"example.com/mono/api"}); err != nil {
		t.Fatalf("Verify on consistent rewrite: %v", err)
	}

	if got := gitStatus(t, fx.Root); got != snapshot {
		t.Errorf("Verify mutated tracked working tree:\nbefore:\n%s\nafter:\n%s", snapshot, got)
	}
}

func TestVerify_inconsistent_missingSymbol(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})

	// Overwrite api/api.go to call a nonexistent storage symbol.
	apiSrc := `package api

import "example.com/mono/storage"

func ApiHello() string {
	return storage.DoesNotExist()
}
`
	writeFile(t, filepath.Join(fx.Root, "modules/api/api.go"), apiSrc)

	rewriteRequire(t, filepath.Join(fx.Root, "modules/api/go.mod"),
		"example.com/mono/storage", "v0.9.0")
	commitAll(t, fx.Root, "release: train/test")

	err := Verify(fx.Root, []string{"example.com/mono/api"})
	if err == nil {
		t.Fatal("Verify succeeded despite broken source; expected failure")
	}
	if !strings.Contains(err.Error(), "DoesNotExist") && !strings.Contains(err.Error(), "undefined") {
		t.Errorf("error should mention the compile failure; got: %v", err)
	}
}

func rewriteRequire(t *testing.T, goModPath, depPath, newVersion string) {
	t.Helper()
	b, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("read %s: %v", goModPath, err)
	}
	lines := strings.Split(string(b), "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "require "+depPath+" ") {
			lines[i] = "require " + depPath + " " + newVersion
		}
	}
	if err := os.WriteFile(goModPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("write %s: %v", goModPath, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func commitAll(t *testing.T, root, msg string) {
	t.Helper()
	run(t, root, "git", "add", "-A")
	run(t, root, "git", "commit", "-m", msg)
}

func gitStatus(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "status", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v: %s", err, out)
	}
	return string(out)
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
