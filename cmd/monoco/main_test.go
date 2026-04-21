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

func TestCLI_release_defaultsDirectToPatchWithoutBump(t *testing.T) {
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

	// No --bump, dry-run: should default storage to patch (v0.1.1), not error.
	out := runCLI(t, bin, fx.Root, "release", "--dry-run", "--slug", "default-patch")
	if !strings.Contains(out, "v0.1.1") || !strings.Contains(out, "example.com/mono/storage") {
		t.Errorf("plan missing default-patch bump for storage:\n%s", out)
	}
}

func TestCLI_init_writesStubManifest(t *testing.T) {
	bin := buildCLI(t)
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})

	// init over an already-initialized fixture: go.work exists, monoco.yaml does not.
	path := filepath.Join(fx.Root, "monoco.yaml")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("pre-condition: monoco.yaml should not exist, got err=%v", err)
	}
	runCLI(t, bin, fx.Root, "init")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("monoco.yaml not written by init: %v", err)
	}

	// Second init leaves the user's existing manifest alone.
	custom := []byte("version: 1\nexclude:\n  - modules/storage\n")
	if err := os.WriteFile(path, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	runCLI(t, bin, fx.Root, "init")
	got, _ := os.ReadFile(path)
	if string(got) != string(custom) {
		t.Errorf("init clobbered an existing monoco.yaml:\n%s", got)
	}
}

func TestCLI_init_singleRootModule(t *testing.T) {
	bin := buildCLI(t)
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/solo\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "solo.go"), "package solo\n\nfunc Hello() string { return \"hi\" }\n")
	runT(t, root, "git", "init", "-q", "-b", "main")
	runT(t, root, "git", "config", "user.email", "t@example.com")
	runT(t, root, "git", "config", "user.name", "t")
	runT(t, root, "git", "add", "-A")
	runT(t, root, "git", "commit", "-q", "-m", "init")

	runCLI(t, bin, root, "init")

	workBytes, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		t.Fatalf("read go.work: %v", err)
	}
	if !strings.Contains(string(workBytes), "\n\t.\n") {
		t.Errorf("go.work missing root use entry; contents:\n%s", workBytes)
	}

	// Downstream command must load the workspace and see the root module.
	out := runCLI(t, bin, root, "affected", "--since", "HEAD")
	_ = out // an empty diff is fine; success == no "empty workspace" / "no such file" error
}

func TestCLI_excludedModuleIgnoredByAffected(t *testing.T) {
	bin := buildCLI(t)
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "private", DependsOn: []string{"storage"}},
		},
	})
	runT(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")
	runT(t, fx.Root, "git", "tag", "modules/private/v0.1.0")

	// Exclude modules/private, then touch storage — affected should list
	// storage but NOT private, even though private depends on it.
	if err := os.WriteFile(filepath.Join(fx.Root, "monoco.yaml"),
		[]byte("version: 1\nexclude:\n  - modules/private\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(fx.Root, "modules/storage/storage.go"),
		"package storage\n\nfunc Hello() string { return \"hi\" }\n")
	runT(t, fx.Root, "git", "add", "-A")
	runT(t, fx.Root, "git", "commit", "-m", "storage edit")

	out := runCLI(t, bin, fx.Root, "affected", "--since", "HEAD~1")
	if !strings.Contains(out, "example.com/mono/storage") {
		t.Errorf("affected missing storage:\n%s", out)
	}
	if strings.Contains(out, "example.com/mono/private") {
		t.Errorf("excluded module appeared in affected:\n%s", out)
	}
}

func TestCLI_taskOverrideFromManifest(t *testing.T) {
	bin := buildCLI(t)
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	// Override `test` with an echo so we don't run real `go test`; the
	// output must show the marker we supplied.
	manifest := "version: 1\ntasks:\n  test:\n    command: [\"/bin/echo\", \"MARKER_XYZ\"]\n"
	if err := os.WriteFile(filepath.Join(fx.Root, "monoco.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runCLI(t, bin, fx.Root, "test", "--all")
	if !strings.Contains(out, "MARKER_XYZ") {
		t.Errorf("task override not applied; output:\n%s", out)
	}
}

func TestCLI_taskPassthroughArgs(t *testing.T) {
	bin := buildCLI(t)
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	// Override `test` with echo so the passthrough args land in stdout
	// verbatim. Verifies that `--` args append to a manifest-overridden
	// base command (the code path is unified with the default-command case).
	manifest := "version: 1\ntasks:\n  test:\n    command: [\"/bin/echo\", \"BASE\"]\n"
	if err := os.WriteFile(filepath.Join(fx.Root, "monoco.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runCLI(t, bin, fx.Root, "test", "--all", "--", "MARKER_PASSTHROUGH", "-count=1")
	if !strings.Contains(out, "BASE MARKER_PASSTHROUGH -count=1") {
		t.Errorf("passthrough args not appended; output:\n%s", out)
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
