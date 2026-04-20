//go:build integration
// +build integration

package integration

import (
	"strings"
	"testing"
)

// TestFixCommitIsPatchBump verifies that the default release behavior
// — no --bump flags — produces a patch bump for a direct-affected
// module and its cascade. (The commit contents are a fix-style tweak;
// the KIND comes from the default, not commit-message parsing.)
func TestFixCommitIsPatchBump(t *testing.T) {
	h := newHarness(t)

	h.writeFix("storage", "FixTweak")
	h.addLocalReplace("storage")

	planOut := h.plan()
	t.Logf("plan:\n%s", planOut)

	storageMod := h.modPath + "/modules/storage"
	apiMod := h.modPath + "/modules/api"

	storageOld, storageNew, storageKind, _ := findPlanEntry(t, planOut, storageMod)
	_, _, apiKind, apiDirect := findPlanEntry(t, planOut, apiMod)

	if storageKind != "patch" {
		t.Errorf("storage kind: want patch (default), got %s", storageKind)
	}
	if apiKind != "patch" {
		t.Errorf("api cascade kind: want patch, got %s", apiKind)
	}
	if apiDirect != "cascade" {
		t.Errorf("api direct: want cascade, got %s", apiDirect)
	}

	// Sanity: new is a patch bump of old.
	wantPrefix := storageOld[:strings.LastIndex(storageOld, ".")+1]
	if !strings.HasPrefix(storageNew, wantPrefix) {
		t.Errorf("storage new=%s is not a patch bump of %s", storageNew, storageOld)
	}

	applyOut := h.apply()
	if !strings.Contains(applyOut, "Pushed to origin") {
		t.Fatalf("apply did not push:\n%s", applyOut)
	}
	h.assertRemoteHasTag("refs/tags/modules/storage/" + storageNew)
}
