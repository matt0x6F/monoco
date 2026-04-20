//go:build integration
// +build integration

package integration

import (
	"strings"
	"testing"
)

// TestFixCommitIsPatchBump verifies that a `fix:` conventional commit
// on storage produces a patch bump for storage and a patch-level
// cascade to api — not a minor bump.
func TestFixCommitIsPatchBump(t *testing.T) {
	h := newHarness(t)

	h.writeFix("storage", "FixTweak")

	planOut := h.plan()
	t.Logf("plan:\n%s", planOut)

	storageMod := h.modPath + "/modules/storage"
	apiMod := h.modPath + "/modules/api"

	storageOld, storageNew, storageKind, _ := findPlanEntry(t, planOut, storageMod)
	_, _, apiKind, apiDirect := findPlanEntry(t, planOut, apiMod)

	if storageKind != "patch" {
		t.Errorf("storage kind: want patch, got %s", storageKind)
	}
	if apiKind != "patch" {
		t.Errorf("api cascade kind: want patch, got %s", apiKind)
	}
	if apiDirect != "cascade" {
		t.Errorf("api direct: want cascade, got %s", apiDirect)
	}

	// Basic sanity: new is a patch bump of old (last number incremented,
	// first two unchanged). storage e.g. v0.1.0 -> v0.1.1.
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
