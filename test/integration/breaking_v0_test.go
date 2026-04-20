//go:build integration
// +build integration

package integration

import (
	"strings"
	"testing"
)

// TestBreakingChangeOnV0 verifies that an explicit --bump=major on a v0
// module ends up as a minor bump (v0.x.y → v0.(x+1).0) rather than
// v1.0.0 — this encodes the pre-v1 instability contract.
//
// The test repo's baseline for all modules is v0.1.0.
func TestBreakingChangeOnV0(t *testing.T) {
	h := newHarness(t)

	h.writeBreaking("storage", "BreakingShape")
	h.addLocalReplace("storage")

	planOut := h.plan("--bump", "modules/storage=major")
	t.Logf("plan:\n%s", planOut)

	storageMod := h.modPath + "/modules/storage"
	storageOld, storageNew, storageKind, _ := findPlanEntry(t, planOut, storageMod)

	if storageKind != "major" {
		t.Errorf("kind column: want major, got %s", storageKind)
	}

	// v0 pre-1.0 rule: major intent on v0.x.y bumps to v0.(x+1).0.
	if !strings.HasPrefix(storageNew, "v0.") {
		t.Errorf("v0 breaking change should stay within v0.x (got %s old=%s)", storageNew, storageOld)
	}
	if storageNew == storageOld {
		t.Errorf("new version should differ from old; got %s == %s", storageNew, storageOld)
	}

	applyOut := h.apply("--bump", "modules/storage=major")
	if !strings.Contains(applyOut, "Pushed to origin") {
		t.Fatalf("apply did not push:\n%s", applyOut)
	}
	h.assertRemoteHasTag("refs/tags/modules/storage/" + storageNew)
}
