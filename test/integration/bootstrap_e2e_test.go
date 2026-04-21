//go:build integration
// +build integration

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBootstrapFirstEverTag covers the "first-ever release for a module
// that has never been tagged" path against the real remote. The shared
// test repo is pre-seeded with tagged modules, so we simulate bootstrap
// by adding a brand-new module on the per-run branch and releasing it:
// `NextVersion("", ...)` must produce v0.1.0, and that tag must land on
// origin atomically with the train tag.
//
// Placeholder-pseudo-version rewriting is exhaustively covered by the
// unit test TestApply_bootstrapFromPlaceholders; this integration test
// focuses on the end-to-end first-tag landing.
func TestBootstrapFirstEverTag(t *testing.T) {
	h := newHarness(t)

	modSuffix := "bootstrap" + strings.ReplaceAll(strings.ReplaceAll(h.runID, "-", ""), "T", "")
	moduleDir := "modules/" + modSuffix
	modulePath := h.modPath + "/" + moduleDir

	h.addFreshModule(modSuffix, modulePath)
	// A feat commit on the new module makes it direct-affected.
	h.writeFeat(modSuffix, "Bootstrap")

	planOut := h.plan("--bump", moduleDir+"=minor")
	t.Logf("plan:\n%s", planOut)

	_, newV, kind, direct := findPlanEntry(t, planOut, modulePath)
	if newV != "v0.1.0" {
		t.Errorf("new version: want v0.1.0, got %s", newV)
	}
	if kind != "minor" {
		t.Errorf("kind: want minor, got %s", kind)
	}
	if direct != "direct" {
		t.Errorf("direct column: want direct, got %s", direct)
	}

	applyOut := h.apply("--bump", moduleDir+"=minor")
	t.Logf("apply:\n%s", applyOut)
	if !strings.Contains(applyOut, "Pushed to origin") {
		t.Fatalf("apply did not push:\n%s", applyOut)
	}

	tag := "refs/tags/" + moduleDir + "/v0.1.0"
	h.assertRemoteHasTag(tag)
	h.assertRemoteHasTag("refs/tags/train/" + todayUTC() + "-" + h.runID)

	// Confirm this is the first time this tag exists — not a re-push of
	// something baselined by newHarness.
	if _, was := h.baselineTags[tag]; was {
		t.Errorf("tag %s existed at baseline; test is not exercising the bootstrap path", tag)
	}
}

// addFreshModule creates a brand-new module directory with a minimal
// go.mod and one .go source file, wires it into go.work via `use`, and
// commits. The module has no prior tag on origin, so the next release
// must bootstrap it at v0.1.0.
func (h *harness) addFreshModule(name, fullModulePath string) {
	h.t.Helper()
	dir := filepath.Join(h.wt, "modules", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatalf("mkdir %s: %v", dir, err)
	}

	goMod := fmt.Sprintf("module %s\n\ngo 1.22\n", fullModulePath)
	mustWrite(h.t, filepath.Join(dir, "go.mod"), goMod)

	// Source file — package name must be a valid identifier; the module
	// directory name includes hex/timestamp characters, so we use a
	// stable package name and let the import path carry the uniqueness.
	src := "package bootstrap\n\n// Marker keeps the module non-empty.\nfunc Marker() string { return \"bootstrap\" }\n"
	mustWrite(h.t, filepath.Join(dir, name+".go"), src)

	// Append a `use` line to go.work. The test repo's go.work already
	// registers the pre-seeded modules; we only add our new one.
	workPath := filepath.Join(h.wt, "go.work")
	existing, err := os.ReadFile(workPath)
	if err != nil {
		h.t.Fatalf("read go.work: %v", err)
	}
	useLine := fmt.Sprintf("\nuse ./modules/%s\n", name)
	if err := os.WriteFile(workPath, append(existing, []byte(useLine)...), 0o644); err != nil {
		h.t.Fatalf("write go.work: %v", err)
	}

	mustRun(h.t, h.wt, "git", "add", "-A")
	mustRun(h.t, h.wt, "git", "commit", "-m", "bootstrap: add fresh module "+name+" for run "+h.runID)
}
