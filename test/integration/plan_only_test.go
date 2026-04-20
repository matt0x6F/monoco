//go:build integration
// +build integration

package integration

import (
	"testing"
)

// TestPlanOnlyDoesNotMutate verifies that `monoco release --dry-run`
// never touches the remote — no branches, no tags, no pushes. It's the
// safety contract that unlocks PR-preview use.
func TestPlanOnlyDoesNotMutate(t *testing.T) {
	h := newHarness(t)

	h.writeFeat("storage", "PlanOnlyProbe")
	h.addLocalReplace("storage")

	before := mustCapture(t, h.wt, "git", "ls-remote", "--refs", "origin",
		"refs/tags/*"+h.runID, "refs/heads/test/"+h.runID)
	if before != "" {
		t.Fatalf("unexpected pre-existing refs for runID %s:\n%s", h.runID, before)
	}

	planOut := h.plan()
	t.Logf("plan:\n%s", planOut)

	after := mustCapture(t, h.wt, "git", "ls-remote", "--refs", "origin",
		"refs/tags/*"+h.runID, "refs/heads/test/"+h.runID)
	if after != "" {
		t.Fatalf("plan mutated remote; refs for runID %s appeared:\n%s", h.runID, after)
	}

	localTags := mustCapture(t, h.wt, "git", "tag", "-l", "*"+h.runID+"*")
	if localTags != "" {
		t.Errorf("plan should not create local tags; got:\n%s", localTags)
	}
}
