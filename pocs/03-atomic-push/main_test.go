package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matt0x6f/monoco/internal/fixture"
)

func TestAtomicPush_happyPath(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})

	// Create a release commit locally.
	if err := os.WriteFile(filepath.Join(fx.Root, "modules/api/go.mod.note"), []byte("release marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runT(t, fx.Root, "git", "add", "-A")
	runT(t, fx.Root, "git", "commit", "-m", "release: train/test")

	tags := []string{
		"modules/storage/v0.9.0",
		"modules/api/v1.4.3",
		"train/2026-04-18-test",
	}
	for _, tg := range tags {
		runT(t, fx.Root, "git", "tag", tg, "HEAD")
	}

	if err := AtomicPush(fx.Root, "origin", "main", tags); err != nil {
		t.Fatalf("AtomicPush: %v", err)
	}

	remoteTags := listRemoteTags(t, fx.RemoteDir)
	for _, tg := range tags {
		if !contains(remoteTags, tg) {
			t.Errorf("remote missing tag %s; has: %v", tg, remoteTags)
		}
	}
}

func TestAtomicPush_rejectedByHook(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})

	// Push main to origin FIRST so the remote has a main ref we can observe.
	// The fixture wires origin but does not push.
	runT(t, fx.Root, "git", "push", "origin", "main")

	// Install a pre-receive hook on the remote that rejects train tags.
	hookPath := filepath.Join(fx.RemoteDir, "hooks", "pre-receive")
	hookBody := `#!/bin/sh
while read oldrev newrev refname; do
  case "$refname" in
    refs/tags/train/*) echo "reject train tag for test" >&2; exit 1 ;;
  esac
done
exit 0
`
	if err := os.WriteFile(hookPath, []byte(hookBody), 0o755); err != nil {
		t.Fatal(err)
	}

	// Capture remote main SHA before the attempted push.
	beforeMain := strings.TrimSpace(runCapture(t, fx.RemoteDir, "git", "rev-parse", "refs/heads/main"))

	// Create release commit + tags locally.
	if err := os.WriteFile(filepath.Join(fx.Root, "modules/api/go.mod.note"), []byte("release marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runT(t, fx.Root, "git", "add", "-A")
	runT(t, fx.Root, "git", "commit", "-m", "release: train/test")

	tags := []string{
		"modules/storage/v0.9.0",
		"modules/api/v1.4.3",
		"train/2026-04-18-test",
	}
	for _, tg := range tags {
		runT(t, fx.Root, "git", "tag", tg, "HEAD")
	}

	err := AtomicPush(fx.Root, "origin", "main", tags)
	if err == nil {
		t.Fatal("AtomicPush succeeded but hook should have rejected")
	}

	remoteTags := listRemoteTags(t, fx.RemoteDir)
	for _, tg := range tags {
		if contains(remoteTags, tg) {
			t.Errorf("remote has tag %s after atomic rejection; should be absent", tg)
		}
	}

	afterMain := strings.TrimSpace(runCapture(t, fx.RemoteDir, "git", "rev-parse", "refs/heads/main"))
	if afterMain != beforeMain {
		t.Errorf("remote main moved from %s to %s despite atomic rejection", beforeMain, afterMain)
	}
}

func listRemoteTags(t *testing.T, remoteDir string) []string {
	t.Helper()
	out := runCapture(t, remoteDir, "git", "tag")
	var tags []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			tags = append(tags, l)
		}
	}
	return tags
}

func runCapture(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
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

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
