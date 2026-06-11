package release

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/matt0x6f/monoco/internal/bump"
	"github.com/matt0x6f/monoco/internal/fixture"
	"github.com/matt0x6f/monoco/internal/workspace"
)

// trailerFixture creates a storage+api fixture with a train tag, so
// commits made by the test land inside the scan window.
func trailerFixture(t *testing.T) *workspace.Workspace {
	t.Helper()
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	gitRun(t, fx.Root, "tag", "train/2026-01-01-base")
	ws, err := workspace.Load(fx.Root)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	return ws
}

func commitWithMessage(t *testing.T, ws *workspace.Workspace, file string, msgParts ...string) {
	t.Helper()
	fileWrite(t, filepath.Join(ws.Root, file), "package storage\n")
	gitRun(t, ws.Root, "add", "-A")
	args := []string{"commit"}
	for _, m := range msgParts {
		args = append(args, "-m", m)
	}
	gitRun(t, ws.Root, args...)
}

func TestTrailerBumps_readsDeclarationsSinceTrainTag(t *testing.T) {
	ws := trailerFixture(t)
	commitWithMessage(t, ws, "modules/storage/a.go",
		"feat(storage): add batch API", "Monoco-Bump: modules/storage=minor")

	merged, decls, since, err := TrailerBumps(ws)
	if err != nil {
		t.Fatalf("TrailerBumps: %v", err)
	}
	if since != "train/2026-01-01-base" {
		t.Errorf("since = %q, want train tag", since)
	}
	if len(decls) != 1 {
		t.Fatalf("decls = %v, want 1", decls)
	}
	if merged["example.com/mono/storage"] != bump.Minor {
		t.Errorf("merged = %v, want storage=minor", merged)
	}
}

// Declarations in commits already shipped (behind the train tag) are
// consumed: they must not leak into the next release.
func TestTrailerBumps_ignoresCommitsBehindTrainTag(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	ws, err := workspace.Load(fx.Root)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	commitWithMessage(t, ws, "modules/storage/a.go",
		"feat(storage): old change", "Monoco-Bump: modules/storage=major")
	gitRun(t, ws.Root, "tag", "train/2026-01-01-shipped")

	merged, decls, _, err := TrailerBumps(ws)
	if err != nil {
		t.Fatalf("TrailerBumps: %v", err)
	}
	if len(decls) != 0 || len(merged) != 0 {
		t.Errorf("pre-train trailer leaked: decls=%v merged=%v", decls, merged)
	}
}

// No train tag yet: full history is scanned.
func TestTrailerBumps_noTrainTagScansFullHistory(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	ws, err := workspace.Load(fx.Root)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	commitWithMessage(t, ws, "modules/storage/a.go",
		"feat(storage): first", "Monoco-Bump: modules/storage=minor")

	merged, _, since, err := TrailerBumps(ws)
	if err != nil {
		t.Fatalf("TrailerBumps: %v", err)
	}
	if since != "" {
		t.Errorf("since = %q, want empty (no train tag)", since)
	}
	if merged["example.com/mono/storage"] != bump.Minor {
		t.Errorf("merged = %v, want storage=minor", merged)
	}
}

// Across commits, the larger kind wins; skip never suppresses an
// explicit release request from another commit.
func TestTrailerBumps_largestKindWins(t *testing.T) {
	ws := trailerFixture(t)
	commitWithMessage(t, ws, "modules/storage/a.go",
		"chore(storage): hold back", "Monoco-Bump: modules/storage=skip")
	commitWithMessage(t, ws, "modules/storage/b.go",
		"fix(storage): patch it", "Monoco-Bump: modules/storage=patch")
	commitWithMessage(t, ws, "modules/storage/c.go",
		"feat(storage): new API", "Monoco-Bump: modules/storage=minor")

	merged, decls, _, err := TrailerBumps(ws)
	if err != nil {
		t.Fatalf("TrailerBumps: %v", err)
	}
	if len(decls) != 3 {
		t.Errorf("decls = %v, want 3", decls)
	}
	if merged["example.com/mono/storage"] != bump.Minor {
		t.Errorf("merged = %v, want minor to win", merged)
	}
}

// GitHub's squash-merge format folds branch commit subjects into a
// bulleted list; a declaration must survive that, and the key is
// case-insensitive.
func TestTrailerBumps_toleratesBulletsAndCase(t *testing.T) {
	ws := trailerFixture(t)
	commitWithMessage(t, ws, "modules/storage/a.go",
		"feat: squashed PR (#42)",
		"* feat(storage): add batch\n* monoco-bump: modules/storage=minor")

	merged, _, _, err := TrailerBumps(ws)
	if err != nil {
		t.Fatalf("TrailerBumps: %v", err)
	}
	if merged["example.com/mono/storage"] != bump.Minor {
		t.Errorf("merged = %v, want storage=minor", merged)
	}
}

// Typos fail closed: a trailer naming an unknown module is an error
// (silently under-releasing is the failure mode this channel must not
// have), and the error names the escape hatch.
func TestTrailerBumps_unknownModuleFailsClosed(t *testing.T) {
	ws := trailerFixture(t)
	commitWithMessage(t, ws, "modules/storage/a.go",
		"feat(storage): typo in trailer", "Monoco-Bump: modules/stroage=minor")

	_, _, _, err := TrailerBumps(ws)
	if err == nil || !strings.Contains(err.Error(), "not found in workspace") {
		t.Fatalf("want unknown-module error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--no-trailers") {
		t.Errorf("error should name the --no-trailers escape hatch: %v", err)
	}
}

func TestTrailerBumps_malformedValueFailsClosed(t *testing.T) {
	ws := trailerFixture(t)
	commitWithMessage(t, ws, "modules/storage/a.go",
		"feat(storage): bad kind", "Monoco-Bump: modules/storage=feature")

	_, _, _, err := TrailerBumps(ws)
	if err == nil || !strings.Contains(err.Error(), "invalid bump kind") {
		t.Fatalf("want invalid-kind error, got: %v", err)
	}
}

// Prose that merely mentions the key mid-sentence is not a declaration.
func TestTrailerBumps_ignoresProseMentions(t *testing.T) {
	ws := trailerFixture(t)
	commitWithMessage(t, ws, "modules/storage/a.go",
		"docs: explain trailers",
		"Use a Monoco-Bump: trailer like Monoco-Bump: modules/storage=minor to declare intent.")

	merged, decls, _, err := TrailerBumps(ws)
	if err != nil {
		t.Fatalf("TrailerBumps: %v", err)
	}
	if len(decls) != 0 || len(merged) != 0 {
		t.Errorf("prose mention treated as declaration: decls=%v merged=%v", decls, merged)
	}
}
