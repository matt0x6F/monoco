//go:build integration
// +build integration

package integration

import (
	"strings"
	"testing"
)

// TestNoChangeIsNoOp verifies that with an empty commit range, plan
// reports no modules, apply short-circuits with "nothing to propagate",
// and neither pushes a branch nor creates any tags.
func TestNoChangeIsNoOp(t *testing.T) {
	h := newHarness(t)
	// Deliberately no commits past origin/main — base == HEAD.

	planOut := h.plan()
	t.Logf("plan:\n%s", planOut)
	if !strings.Contains(planOut, "(no modules affected)") {
		t.Errorf("plan output should report no modules affected:\n%s", planOut)
	}

	applyOut := h.apply()
	t.Logf("apply:\n%s", applyOut)
	if !strings.Contains(applyOut, "nothing to propagate") {
		t.Errorf("apply should short-circuit with 'nothing to propagate':\n%s", applyOut)
	}
	if strings.Contains(applyOut, "Pushed to origin") {
		t.Errorf("apply should not push on a no-op:\n%s", applyOut)
	}

	// No tags should have been created for this runID.
	tags := h.remoteTagsForRun()
	if len(tags) != 0 {
		t.Errorf("expected no remote tags for runID %s, got %v", h.runID, tags)
	}
}
