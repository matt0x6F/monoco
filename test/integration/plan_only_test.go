//go:build integration
// +build integration

package integration

import (
	"testing"
)

// TestPlanOnlyDoesNotMutate verifies that running `propagate plan`
// (no apply) never touches the remote — no branches, no tags, no
// pushes. It's the safety contract that unlocks PR-preview use.
func TestPlanOnlyDoesNotMutate(t *testing.T) {
	h := newHarness(t)

	h.writeFeat("storage", "PlanOnlyProbe")

	// Snapshot all refs matching this runID BEFORE plan — should be empty.
	before := mustCapture(t, h.wt, "git", "ls-remote", "--refs", "origin",
		"refs/tags/*"+h.runID, "refs/heads/test/"+h.runID)
	if before != "" {
		t.Fatalf("unexpected pre-existing refs for runID %s:\n%s", h.runID, before)
	}

	planOut := h.plan()
	t.Logf("plan:\n%s", planOut)

	// Post-plan snapshot — must still be empty. Plan is read-only.
	after := mustCapture(t, h.wt, "git", "ls-remote", "--refs", "origin",
		"refs/tags/*"+h.runID, "refs/heads/test/"+h.runID)
	if after != "" {
		t.Fatalf("plan mutated remote; refs for runID %s appeared:\n%s", h.runID, after)
	}

	// No local tags either.
	localTags := mustCapture(t, h.wt, "git", "tag", "-l", "*"+h.runID+"*")
	if localTags != "" {
		t.Errorf("plan should not create local tags; got:\n%s", localTags)
	}
}
