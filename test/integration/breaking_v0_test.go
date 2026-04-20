//go:build integration
// +build integration

package integration

import (
	"strings"
	"testing"
)

// TestBreakingChangeOnV0 verifies a `feat!:` commit is classified as
// a major bump in the plan, but — per Go semver convention for v0 —
// the actual version bump is minor (v0.x.y → v0.(x+1).0) rather than
// v1.0.0. This encodes the pre-v1 instability contract.
//
// The test repo's baseline for all modules is v0.1.0.
func TestBreakingChangeOnV0(t *testing.T) {
	h := newHarness(t)

	h.writeBreaking("storage", "BreakingShape")

	planOut := h.plan()
	t.Logf("plan:\n%s", planOut)

	storageMod := h.modPath + "/modules/storage"
	storageOld, storageNew, storageKind, _ := findPlanEntry(t, planOut, storageMod)

	if storageKind != "major" {
		t.Errorf("kind column: want major, got %s", storageKind)
	}

	// v0 pre-1.0 rule: a major-intent commit on v0.x.y bumps to
	// v0.(x+1).0, NOT v1.0.0. If the baseline tag strategy ever
	// changes, update this test.
	if !strings.HasPrefix(storageNew, "v0.") {
		t.Errorf("v0 breaking change should stay within v0.x (got %s old=%s)", storageNew, storageOld)
	}
	if storageNew == storageOld {
		t.Errorf("new version should differ from old; got %s == %s", storageNew, storageOld)
	}

	applyOut := h.apply()
	if !strings.Contains(applyOut, "Pushed to origin") {
		t.Fatalf("apply did not push:\n%s", applyOut)
	}
	h.assertRemoteHasTag("refs/tags/modules/storage/" + storageNew)
}
