//go:build integration
// +build integration

package integration

import (
	"strings"
	"testing"
)

// TestNoChangeIsNoOp verifies that with no workspace-local `replace`
// directives, `monoco release` short-circuits with "nothing to release"
// — no branches pushed, no tags created.
func TestNoChangeIsNoOp(t *testing.T) {
	h := newHarness(t)
	// Deliberately no edits and no `replace` directives.

	planOut := h.plan()
	t.Logf("plan:\n%s", planOut)
	if !strings.Contains(planOut, "nothing to release") {
		t.Errorf("plan should report 'nothing to release':\n%s", planOut)
	}

	applyOut := h.apply()
	t.Logf("apply:\n%s", applyOut)
	if !strings.Contains(applyOut, "nothing to release") {
		t.Errorf("apply should short-circuit with 'nothing to release':\n%s", applyOut)
	}
	if strings.Contains(applyOut, "Pushed to origin") {
		t.Errorf("apply should not push on a no-op:\n%s", applyOut)
	}

	tags := h.remoteTagsForRun()
	if len(tags) != 0 {
		t.Errorf("expected no remote tags for runID %s, got %v", h.runID, tags)
	}
}
