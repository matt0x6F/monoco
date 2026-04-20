package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matt0x6f/monoco/internal/fixture"
)

func TestCLI_endToEnd(t *testing.T) {
	// Build the binary once.
	bin := buildCLI(t)

	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	runT(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")
	runT(t, fx.Root, "git", "tag", "modules/api/v0.1.0")
	runT(t, fx.Root, "git", "push", "origin", "main", "--tags")

	// A feat commit on storage.
	writeFile(t, filepath.Join(fx.Root, "modules/storage/storage.go"),
		"package storage\n\nfunc StorageHello() string { return \"new\" }\nfunc Batch() string { return \"b\" }\n")
	runT(t, fx.Root, "git", "add", "-A")
	runT(t, fx.Root, "git", "commit", "-m", "feat(storage): add batch")

	// `monoco affected --since HEAD~1`
	out := runCLI(t, bin, fx.Root, "affected", "--since", "HEAD~1")
	if !strings.Contains(out, "example.com/mono/storage") || !strings.Contains(out, "example.com/mono/api") {
		t.Errorf("affected missing expected modules; got: %s", out)
	}

	// `monoco test --since HEAD~1` — task runner fanout.
	out = runCLI(t, bin, fx.Root, "test", "--since", "HEAD~1")
	for _, expected := range []string{"example.com/mono/storage", "example.com/mono/api"} {
		if !strings.Contains(out, expected) {
			t.Errorf("test output missing %s; got:\n%s", expected, out)
		}
	}

	// `monoco propagate plan --since HEAD~1`
	out = runCLI(t, bin, fx.Root, "propagate", "plan", "--since", "HEAD~1")
	if !strings.Contains(out, "v0.2.0") {
		t.Errorf("plan output missing v0.2.0; got: %s", out)
	}

	// `monoco propagate plan --since HEAD~1 --show-diffs` — summary plus
	// unified diff for api/go.mod (the only entry with an in-plan require to rewrite).
	out = runCLI(t, bin, fx.Root, "propagate", "plan", "--since", "HEAD~1", "--show-diffs")
	for _, want := range []string{
		"MODULE",
		"--- modules/api/go.mod",
		"+++ modules/api/go.mod (proposed)",
		"+require example.com/mono/storage v0.2.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan --show-diffs missing %q; got:\n%s", want, out)
		}
	}

	// `monoco propagate apply --since HEAD~1 --remote=origin`
	out = runCLI(t, bin, fx.Root, "propagate", "apply", "--since", "HEAD~1", "--remote", "origin", "--slug", "e2e")
	if !strings.Contains(out, "Pushed to origin") {
		t.Errorf("apply did not push; got: %s", out)
	}
}

func buildCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "monoco")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/matt0x6f/monoco/cmd/monoco")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	return bin
}

func runCLI(t *testing.T, bin, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("monoco %v: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runT(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
