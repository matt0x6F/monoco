//go:build integration
// +build integration

package integration

import (
	"strings"
	"testing"
)

// TestMultiModuleFeat verifies that two direct changes (each with its
// own workspace-local replace) produce two "direct" entries plus any
// cascades, all tagged on the same release commit.
//
// storage (leaf) and auth (sibling) both get a direct change; api
// cascades from storage.
func TestMultiModuleFeat(t *testing.T) {
	h := newHarness(t)

	h.writeFeat("storage", "MultiStorage")
	h.writeFeat("auth", "MultiAuth")
	h.addLocalReplace("storage")
	h.addLocalReplace("auth")

	planOut := h.plan(
		"--bump", "modules/storage=minor",
		"--bump", "modules/auth=minor",
	)
	t.Logf("plan:\n%s", planOut)

	storageMod := h.modPath + "/modules/storage"
	authMod := h.modPath + "/modules/auth"
	apiMod := h.modPath + "/modules/api"

	_, storageNew, _, storageDirect := findPlanEntry(t, planOut, storageMod)
	_, authNew, _, authDirect := findPlanEntry(t, planOut, authMod)
	_, apiNew, _, apiDirect := findPlanEntry(t, planOut, apiMod)

	if storageDirect != "direct" {
		t.Errorf("storage direct column: want direct, got %s", storageDirect)
	}
	if authDirect != "direct" {
		t.Errorf("auth direct column: want direct, got %s", authDirect)
	}
	if apiDirect != "cascade" {
		t.Errorf("api direct column: want cascade (via storage), got %s", apiDirect)
	}

	applyOut := h.apply(
		"--bump", "modules/storage=minor",
		"--bump", "modules/auth=minor",
	)
	if !strings.Contains(applyOut, "Pushed to origin") {
		t.Fatalf("apply did not push:\n%s", applyOut)
	}

	for _, tag := range []string{
		"refs/tags/modules/storage/" + storageNew,
		"refs/tags/modules/auth/" + authNew,
		"refs/tags/modules/api/" + apiNew,
		"refs/tags/train/" + todayUTC() + "-" + h.runID,
	} {
		h.assertRemoteHasTag(tag)
	}

	// api consumes storage (and nothing else in the plan), so the
	// release commit must update api/go.mod and populate api/go.sum
	// with the precomputed h1: line for storage@<new>.
	h.assertReleaseCommitTouches("api", "go.mod")
	h.assertReleaseCommitTouches("api", "go.sum")
}
