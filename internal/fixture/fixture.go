// Package fixture produces disposable on-disk monorepos for POCs and tests.
package fixture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ModuleSpec declares one module in a fixture.
type ModuleSpec struct {
	Name      string   // short name; module path becomes example.com/mono/<name>
	DependsOn []string // names of modules this one requires
}

// Spec describes a fixture to be created.
type Spec struct {
	Modules []ModuleSpec
}

// Fixture is a materialized on-disk monorepo. Cleaned up automatically via t.Cleanup.
type Fixture struct {
	Root      string // working tree root
	RemoteDir string // bare remote directory (file://)
}

// New creates a fixture in a fresh temp dir and returns its paths.
// The git repo is initialized, modules are committed, and origin is wired to a local bare remote.
func New(t *testing.T, spec Spec) *Fixture {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(t.TempDir(), "remote.git")

	// Bare remote.
	mustRun(t, "", "git", "init", "--bare", remote)

	// Working tree.
	mustRun(t, root, "git", "init")
	mustRun(t, root, "git", "checkout", "-b", "main")
	mustRun(t, root, "git", "config", "user.email", "fixture@monoco.test")
	mustRun(t, root, "git", "config", "user.name", "fixture")
	mustRun(t, root, "git", "remote", "add", "origin", remote)

	// Modules.
	for _, m := range spec.Modules {
		writeModule(t, root, m)
	}
	writeWorkspace(t, root, spec)

	// Initial commit.
	mustRun(t, root, "git", "add", "-A")
	mustRun(t, root, "git", "commit", "-m", "initial fixture")

	return &Fixture{Root: root, RemoteDir: remote}
}

func writeModule(t *testing.T, root string, m ModuleSpec) {
	t.Helper()
	dir := filepath.Join(root, "modules", m.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}

	// go.mod
	var b strings.Builder
	fmt.Fprintf(&b, "module example.com/mono/%s\n\ngo 1.22\n", m.Name)
	for _, dep := range m.DependsOn {
		// Pseudo-version placeholder; workspace mode will resolve to local path.
		fmt.Fprintf(&b, "\nrequire example.com/mono/%s v0.0.0-00010101000000-000000000000\n", dep)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), b.String())

	// Source file with one exported function.
	funcName := strings.ToUpper(m.Name[:1]) + m.Name[1:] + "Hello"
	var src strings.Builder
	fmt.Fprintf(&src, "package %s\n\n", m.Name)
	for _, dep := range m.DependsOn {
		fmt.Fprintf(&src, "import \"example.com/mono/%s\"\n\n", dep)
	}
	fmt.Fprintf(&src, "func %s() string {\n", funcName)
	if len(m.DependsOn) == 0 {
		fmt.Fprintf(&src, "\treturn %q\n", m.Name)
	} else {
		dep := m.DependsOn[0]
		depFunc := strings.ToUpper(dep[:1]) + dep[1:] + "Hello"
		fmt.Fprintf(&src, "\treturn %q + \" \" + %s.%s()\n", m.Name, dep, depFunc)
	}
	fmt.Fprintf(&src, "}\n")
	writeFile(t, filepath.Join(dir, m.Name+".go"), src.String())

	// Test.
	var test strings.Builder
	fmt.Fprintf(&test, "package %s\n\nimport \"testing\"\n\n", m.Name)
	fmt.Fprintf(&test, "func Test%s(t *testing.T) {\n", funcName)
	fmt.Fprintf(&test, "\tif %s() == \"\" {\n\t\tt.Fatal(\"empty\")\n\t}\n}\n", funcName)
	writeFile(t, filepath.Join(dir, m.Name+"_test.go"), test.String())
}

func writeWorkspace(t *testing.T, root string, spec Spec) {
	t.Helper()
	var b strings.Builder
	b.WriteString("go 1.22\n\nuse (\n")
	for _, m := range spec.Modules {
		fmt.Fprintf(&b, "\t./modules/%s\n", m.Name)
	}
	b.WriteString(")\n")
	writeFile(t, filepath.Join(root, "go.work"), b.String())
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustRun(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
