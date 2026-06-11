package gitgraph

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/matt0x6f/monoco/internal/fixture"
)

func TestTouchedFiles_returnsChangedPathsInRange(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}, {Name: "auth"}},
	})
	base := headSHA(t, fx.Root)

	// Modify storage on a new commit.
	writeFile(t, filepath.Join(fx.Root, "modules/storage/storage.go"), "package storage\n\nfunc StorageHello() string { return \"edited\" }\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "feat(storage): edit")

	files, err := TouchedFiles(fx.Root, base, "HEAD")
	if err != nil {
		t.Fatalf("TouchedFiles: %v", err)
	}
	found := false
	for _, f := range files {
		if f == "modules/storage/storage.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected modules/storage/storage.go in touched files; got %v", files)
	}
}

func TestCommitsInRange_returnsSubjectsForPath(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}, {Name: "auth"}},
	})
	base := headSHA(t, fx.Root)

	writeFile(t, filepath.Join(fx.Root, "modules/storage/extra.go"), "package storage\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "feat(storage): add extra")

	writeFile(t, filepath.Join(fx.Root, "modules/auth/extra.go"), "package auth\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "fix(auth): tweak")

	// Only commits that touched modules/storage.
	commits, err := CommitsInRange(fx.Root, base, "HEAD", "modules/storage")
	if err != nil {
		t.Fatalf("CommitsInRange: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("expected 1 commit touching storage; got %d: %v", len(commits), commits)
	}
	if commits[0].Subject != "feat(storage): add extra" {
		t.Errorf("unexpected subject: %q", commits[0].Subject)
	}
}

func TestLatestTagForModule_missingReturnsEmpty(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	tag, err := LatestTagForModule(fx.Root, "modules/storage")
	if err != nil {
		t.Fatalf("LatestTagForModule: %v", err)
	}
	if tag != "" {
		t.Errorf("expected empty tag on unreleased module; got %q", tag)
	}
}

func TestLatestTagForModule_picksHighestSemver(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	run(t, fx.Root, "git", "tag", "modules/storage/v0.1.0")
	run(t, fx.Root, "git", "tag", "modules/storage/v0.2.0")
	run(t, fx.Root, "git", "tag", "modules/storage/v0.1.5")

	tag, err := LatestTagForModule(fx.Root, "modules/storage")
	if err != nil {
		t.Fatalf("LatestTagForModule: %v", err)
	}
	if tag != "v0.2.0" {
		t.Errorf("expected v0.2.0; got %q", tag)
	}
}

func TestLatestTrainTag(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})

	tag, err := LatestTrainTag(fx.Root)
	if err != nil {
		t.Fatalf("LatestTrainTag: %v", err)
	}
	if tag != "" {
		t.Errorf("expected no train tag; got %q", tag)
	}

	run(t, fx.Root, "git", "tag", "train/2026-01-01-first")
	writeFile(t, filepath.Join(fx.Root, "modules/storage/extra.go"), "package storage\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "feat: more")
	run(t, fx.Root, "git", "tag", "train/2026-02-01-second")

	tag, err = LatestTrainTag(fx.Root)
	if err != nil {
		t.Fatalf("LatestTrainTag: %v", err)
	}
	if tag != "train/2026-02-01-second" {
		t.Errorf("expected train/2026-02-01-second; got %q", tag)
	}
}

// A train tag that is not an ancestor of HEAD (e.g. cut on main while
// we release from a maintenance branch) must not anchor the scan.
func TestLatestTrainTag_ignoresUnreachable(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	run(t, fx.Root, "git", "tag", "train/2026-01-01-shared")
	run(t, fx.Root, "git", "checkout", "-q", "-b", "release-1.x")

	// Back on main, a newer train lands that release-1.x can't see.
	run(t, fx.Root, "git", "checkout", "-q", "main")
	writeFile(t, filepath.Join(fx.Root, "modules/storage/main_only.go"), "package storage\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "feat: main only")
	run(t, fx.Root, "git", "tag", "train/2026-03-01-main-only")

	run(t, fx.Root, "git", "checkout", "-q", "release-1.x")
	tag, err := LatestTrainTag(fx.Root)
	if err != nil {
		t.Fatalf("LatestTrainTag: %v", err)
	}
	if tag != "train/2026-01-01-shared" {
		t.Errorf("expected train/2026-01-01-shared; got %q", tag)
	}
}

func TestCommitsSince(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	run(t, fx.Root, "git", "tag", "train/2026-01-01-base")

	writeFile(t, filepath.Join(fx.Root, "modules/storage/extra.go"), "package storage\n")
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "feat: after train")

	commits, err := CommitsSince(fx.Root, "train/2026-01-01-base")
	if err != nil {
		t.Fatalf("CommitsSince: %v", err)
	}
	if len(commits) != 1 || commits[0].Subject != "feat: after train" {
		t.Errorf("expected only the post-train commit; got %v", commits)
	}

	// Empty ref scans full history (fixture has at least 2 commits now).
	all, err := CommitsSince(fx.Root, "")
	if err != nil {
		t.Fatalf("CommitsSince(\"\"): %v", err)
	}
	if len(all) < 2 {
		t.Errorf("expected full history; got %v", all)
	}
}

func headSHA(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse: %v: %s", err, out)
	}
	s := string(out)
	if n := len(s); n > 0 && s[n-1] == '\n' {
		s = s[:n-1]
	}
	return s
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
