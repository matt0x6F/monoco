//go:build integration
// +build integration

package integration

import (
	"strings"
	"testing"
)

// TestMultiModuleFeat verifies that two direct changes in the same
// commit range produce two "direct" entries plus any cascades, and
// that all three modules get tagged on the same release commit.
//
// storage (leaf) and auth (sibling) both get a direct feat; api
// cascades from storage.
func TestMultiModuleFeat(t *testing.T) {
	h := newHarness(t)

	h.writeFeat("storage", "MultiStorage")
	h.writeFeat("auth", "MultiAuth")

	planOut := h.plan()
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

	applyOut := h.apply()
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
}
