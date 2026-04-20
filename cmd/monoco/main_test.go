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

func TestCLI_endToEnd_graphAndTasks(t *testing.T) {
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

	writeFile(t, filepath.Join(fx.Root, "modules/storage/storage.go"),
		"package storage\n\nfunc StorageHello() string { return \"new\" }\nfunc Batch() string { return \"b\" }\n")
	runT(t, fx.Root, "git", "add", "-A")
	runT(t, fx.Root, "git", "commit", "-m", "storage: add batch")

	out := runCLI(t, bin, fx.Root, "affected", "--since", "HEAD~1")
	if !strings.Contains(out, "example.com/mono/storage") || !strings.Contains(out, "example.com/mono/api") {
		t.Errorf("affected missing expected modules; got: %s", out)
	}

	out = runCLI(t, bin, fx.Root, "test", "--since", "HEAD~1")
	for _, expected := range []string{"example.com/mono/storage", "example.com/mono/api"} {
		if !strings.Contains(out, expected) {
			t.Errorf("test output missing %s; got:\n%s", expected, out)
		}
	}
}

func TestCLI_release_withBumpAndReplace(t *testing.T) {
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

	// Add a workspace-local replace in api/go.mod so storage becomes direct-affected.
	apiMod := filepath.Join(fx.Root, "modules/api/go.mod")
	b, err := os.ReadFile(apiMod)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(apiMod, []byte(string(b)+"\nreplace example.com/mono/storage => ../storage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Also change storage content so the release is meaningful.
	writeFile(t, filepath.Join(fx.Root, "modules/storage/storage.go"),
		"package storage\n\nfunc StorageHello() string { return \"new\" }\n")
	runT(t, fx.Root, "git", "add", "-A")
	runT(t, fx.Root, "git", "commit", "-m", "wip: storage change with local replace")

	// Dry-run first: prints plan, no tags created.
	out := runCLI(t, bin, fx.Root,
		"release", "--dry-run",
		"--bump", "modules/storage=minor",
		"--slug", "e2e",
	)
	for _, want := range []string{"example.com/mono/storage", "v0.2.0", "MODULE"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run missing %q; got:\n%s", want, out)
		}
	}
	if tagExists(t, fx.Root, "modules/storage/v0.2.0") {
		t.Error("dry-run must not create tags")
	}

	// Real run with -y to skip the Proceed? prompt.
	out = runCLI(t, bin, fx.Root,
		"release",
		"-y",
		"--bump", "modules/storage=minor",
		"--slug", "e2e",
	)
	if !strings.Contains(out, "Pushed to origin") {
		t.Errorf("release did not push; got: %s", out)
	}
	for _, tg := range []string{"modules/storage/v0.2.0", "modules/api/v0.1.1"} {
		if !tagExists(t, fx.Root, tg) {
			t.Errorf("missing tag %s", tg)
		}
	}

	// api/go.mod no longer has the replace; does have the new require.
	newAPI, _ := os.ReadFile(apiMod)
	if strings.Contains(string(newAPI), "replace example.com/mono/storage") {
		t.Errorf("replace not stripped:\n%s", newAPI)
	}
	if !strings.Contains(string(newAPI), "example.com/mono/storage v0.2.0") {
		t.Errorf("require not bumped:\n%s", newAPI)
	}
	// api/go.sum has the storage hash lines.
	apiSum, err := os.ReadFile(filepath.Join(fx.Root, "modules/api/go.sum"))
	if err != nil {
		t.Fatalf("read api/go.sum: %v", err)
	}
	if !strings.Contains(string(apiSum), "example.com/mono/storage v0.2.0 h1:") {
		t.Errorf("api/go.sum missing h1: line:\n%s", apiSum)
	}
}

func TestCLI_release_failsClosedWithoutBump(t *testing.T) {
	bin := buildCLI(t)

	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	runT(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")
	runT(t, fx.Root, "git", "tag", "modules/api/v0.1.0")

	apiMod := filepath.Join(fx.Root, "modules/api/go.mod")
	b, _ := os.ReadFile(apiMod)
	if err := os.WriteFile(apiMod, []byte(string(b)+"\nreplace example.com/mono/storage => ../storage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runT(t, fx.Root, "git", "add", "-A")
	runT(t, fx.Root, "git", "commit", "-m", "wip: add replace")

	// Non-interactive (stdin is a pipe) with no --bump → must error.
	cmd := exec.Command(bin, "release", "--dry-run")
	cmd.Dir = fx.Root
	cmd.Stdin = strings.NewReader("")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("expected failure when no --bump and no TTY")
	}
	if !strings.Contains(stderr.String(), "no bump specified") && !strings.Contains(stderr.String(), "supply --bump") {
		t.Errorf("error should mention missing bump; stderr:\n%s", stderr.String())
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

func tagExists(t *testing.T, root, tag string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "tag", "-l", tag)
	out, _ := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)) == tag
}
