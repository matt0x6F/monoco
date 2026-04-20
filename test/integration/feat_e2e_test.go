//go:build integration
// +build integration

package integration

import (
	"strings"
	"testing"
)

// TestFeatEndToEnd is the flagship scenario: change storage (with a
// workspace-local replace in api pointing at it), release it with an
// explicit --bump minor, verify plan includes the direct change and a
// cascaded patch on its dependent, apply pushes to origin, and a fresh
// consumer outside the monorepo can `go get` and build against the new
// tag.
func TestFeatEndToEnd(t *testing.T) {
	h := newHarness(t)

	h.writeFeat("storage", "Batch")
	h.addLocalReplace("storage")

	planOut := h.plan("--bump", "modules/storage=minor")
	t.Logf("plan:\n%s", planOut)

	storageMod := h.modPath + "/modules/storage"
	apiMod := h.modPath + "/modules/api"
	authMod := h.modPath + "/modules/auth"

	_, storageNew, storageKind, storageDirect := findPlanEntry(t, planOut, storageMod)
	_, apiNew, _, apiDirect := findPlanEntry(t, planOut, apiMod)

	if storageKind != "minor" {
		t.Errorf("storage kind: want minor, got %s", storageKind)
	}
	if storageDirect != "direct" {
		t.Errorf("storage direct column: want direct, got %s", storageDirect)
	}
	if apiDirect != "cascade" {
		t.Errorf("api direct column: want cascade, got %s", apiDirect)
	}
	if strings.Contains(planOut, authMod) {
		t.Errorf("plan should NOT include auth (no storage dep):\n%s", planOut)
	}

	applyOut := h.apply("--bump", "modules/storage=minor")
	t.Logf("apply:\n%s", applyOut)
	if !strings.Contains(applyOut, "Pushed to origin") {
		t.Fatalf("apply did not push:\n%s", applyOut)
	}

	h.assertRemoteHasTag("refs/tags/modules/storage/" + storageNew)
	h.assertRemoteHasTag("refs/tags/modules/api/" + apiNew)
	h.assertRemoteHasTag("refs/tags/train/" + todayUTC() + "-" + h.runID)

	// The release commit must also carry api/go.sum — precomputed h1:
	// lines for the freshly-tagged storage version. Without this, a
	// consumer building against api@<new> with -mod=readonly fails.
	h.assertReleaseCommitTouches("api", "go.mod")
	h.assertReleaseCommitTouches("api", "go.sum")

	// External-consumer probe — the highest-confidence check.
	h.consumerProbe(apiNew)
	t.Logf("consumer build succeeded against %s/modules/api@%s", h.modPath, apiNew)
}
