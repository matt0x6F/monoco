//go:build integration
// +build integration

package integration

import (
	"strings"
	"testing"
)

// TestVerificationFailureRollsBack verifies the atomic-release contract:
// when `monoco release` detects that a module won't compile in module
// mode (with a replace-directive shim pointing at the new pseudo-tag),
// it rolls the release commit back and does NOT push tags.
//
// We force this by making api reference a storage symbol that doesn't
// exist. git will happily commit broken code; monoco's verify step is
// what catches it.
func TestVerificationFailureRollsBack(t *testing.T) {
	h := newHarness(t)

	// A clean change on storage so the release has real work to do.
	h.writeFeat("storage", "NormalStorage")
	// A broken commit on api — references storage.Nonexistent_<runID>.
	h.writeBreakingAPI()
	// Pin storage locally so release picks it up as direct-affected.
	h.addLocalReplace("storage")

	planOut := h.plan()
	t.Logf("plan:\n%s", planOut)

	stdout, stderr := h.applyExpectFail()
	t.Logf("apply stdout:\n%s\napply stderr:\n%s", stdout, stderr)

	h.assertRemoteMissingTag("refs/tags/train/" + todayUTC() + "-" + h.runID)
	h.assertRemoteMissingTag("refs/tags/modules/storage/v0.1.1")
	h.assertRemoteMissingTag("refs/tags/modules/storage/v0.2.0")
	h.assertRemoteMissingTag("refs/tags/modules/api/v0.1.1")

	// Local branch HEAD should NOT be a release commit — auto-rollback
	// restored pre-run state. (It will be the workspace-local-replace
	// commit added by addLocalReplace, not the "release:" commit.)
	head := trim(mustCapture(t, h.wt, "git", "log", "-1", "--format=%s"))
	if strings.HasPrefix(head, "release:") {
		t.Errorf("local HEAD is a release commit — rollback failed; subject=%q", head)
	}
}
