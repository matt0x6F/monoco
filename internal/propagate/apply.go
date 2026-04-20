package propagate

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/matt0x6f/monoco/internal/workspace"
	"golang.org/x/mod/modfile"
)

// ApplyOptions configures apply.
type ApplyOptions struct {
	Remote string // e.g. "origin". If empty, skip push.
	Branch string // optional; defaults to current branch
}

// ApplyResult reports what happened.
type ApplyResult struct {
	ReleaseCommit string   // SHA of the release commit
	Tags          []string // all tags created (module tags + train)
	Pushed        bool
}

// Apply executes plan against ws. On pre-push failure (rewrite, verify),
// any partial state is rolled back. Returns an ApplyResult on success.
func Apply(ws *workspace.Workspace, plan *Plan, opts ApplyOptions) (*ApplyResult, error) {
	if len(plan.Entries) == 0 {
		return nil, fmt.Errorf("empty plan")
	}

	// Snapshot HEAD for rollback.
	oldHead, err := shellGit(ws.Root, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	oldHead = trim(oldHead)

	// 1. Rewrite go.mods in topo order.
	if err := rewriteGoMods(ws, plan); err != nil {
		// Nothing committed yet — just restore files from HEAD.
		_, _ = shellGit(ws.Root, "checkout", "--", ".")
		return nil, fmt.Errorf("rewrite go.mods: %w", err)
	}

	// 2. Create ONE release commit.
	if _, err := shellGit(ws.Root, "add", "-A"); err != nil {
		return nil, err
	}
	if _, err := shellGit(ws.Root, "commit", "-m", plan.CommitMsg); err != nil {
		return nil, fmt.Errorf("create release commit: %w", err)
	}

	// 3. Verify in module mode. If this fails, reset --hard to oldHead.
	paths := plan.ModulePaths()
	if err := Verify(ws, paths); err != nil {
		if _, rerr := shellGit(ws.Root, "reset", "--hard", oldHead); rerr != nil {
			return nil, fmt.Errorf("verify failed AND rollback failed: verify=%w, rollback=%v", err, rerr)
		}
		return nil, fmt.Errorf("verify: %w", err)
	}

	releaseSHAOut, err := shellGit(ws.Root, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	releaseSHA := trim(releaseSHAOut)

	// 4. Create tags. If a tag already exists at the release commit
	// (re-run after push failure), skip it quietly.
	var tags []string
	for _, e := range plan.Entries {
		if tagAlreadyAt(ws.Root, e.TagName, releaseSHA) {
			tags = append(tags, e.TagName)
			continue
		}
		if _, err := shellGit(ws.Root, "tag", e.TagName, releaseSHA); err != nil {
			return nil, fmt.Errorf("tag %s: %w", e.TagName, err)
		}
		tags = append(tags, e.TagName)
	}
	if !tagAlreadyAt(ws.Root, plan.TrainTag, releaseSHA) {
		if _, err := shellGit(ws.Root, "tag", plan.TrainTag, releaseSHA); err != nil {
			return nil, fmt.Errorf("tag %s: %w", plan.TrainTag, err)
		}
	}
	tags = append(tags, plan.TrainTag)

	result := &ApplyResult{ReleaseCommit: releaseSHA, Tags: tags}

	// 5. Atomic push (optional).
	if opts.Remote == "" {
		return result, nil
	}
	branch := opts.Branch
	if branch == "" {
		branch, err = currentBranch(ws.Root)
		if err != nil {
			return result, fmt.Errorf("detect branch: %w", err)
		}
	}
	if err := atomicPush(ws.Root, opts.Remote, branch, tags); err != nil {
		// Do NOT rollback on push failure; local tags + commit are fine
		// and the user can retry Apply which will skip already-present tags.
		return result, fmt.Errorf("atomic push: %w", err)
	}
	result.Pushed = true
	return result, nil
}

// RewrittenMod is the proposed result of rewriting a single go.mod.
// Only modules whose go.mod actually changes are returned by ComputeRewrites.
type RewrittenMod struct {
	GoModPath string // absolute path to go.mod
	Old       []byte // current bytes on disk
	New       []byte // proposed bytes after applying the plan's version bumps
}

// ComputeRewrites returns the proposed go.mod rewrites for plan, in memory.
// It does not touch the filesystem. Both Apply and `propagate plan --show-diffs`
// consume this so the preview is byte-identical to what apply writes.
//
// The returned map is keyed by Entry.ModulePath. Modules whose go.mod has no
// in-plan require to update are omitted (no change to write).
func ComputeRewrites(ws *workspace.Workspace, plan *Plan) (map[string]RewrittenMod, error) {
	newVersions := map[string]string{}
	for _, e := range plan.Entries {
		newVersions[e.ModulePath] = e.NewVersion
	}
	out := map[string]RewrittenMod{}
	for _, e := range plan.Entries {
		goModPath := filepath.Join(ws.Modules[e.ModulePath].Dir, "go.mod")
		b, err := os.ReadFile(goModPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", goModPath, err)
		}
		mf, err := modfile.Parse(goModPath, b, nil)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", goModPath, err)
		}
		changed := false
		for _, req := range mf.Require {
			if newV, inPlan := newVersions[req.Mod.Path]; inPlan {
				if err := mf.AddRequire(req.Mod.Path, newV); err != nil {
					return nil, fmt.Errorf("update require %s: %w", req.Mod.Path, err)
				}
				changed = true
			}
		}
		if !changed {
			continue
		}
		mf.Cleanup()
		formatted, err := mf.Format()
		if err != nil {
			return nil, fmt.Errorf("format %s: %w", goModPath, err)
		}
		out[e.ModulePath] = RewrittenMod{GoModPath: goModPath, Old: b, New: formatted}
	}
	return out, nil
}

// rewriteGoMods rewrites each entry's require lines to new versions of
// other entries also in the plan.
func rewriteGoMods(ws *workspace.Workspace, plan *Plan) error {
	rewrites, err := ComputeRewrites(ws, plan)
	if err != nil {
		return err
	}
	for _, r := range rewrites {
		if err := os.WriteFile(r.GoModPath, r.New, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", r.GoModPath, err)
		}
	}
	return nil
}

func atomicPush(root, remote, branch string, tags []string) error {
	args := []string{"push", "--atomic", remote, branch}
	for _, t := range tags {
		args = append(args, "refs/tags/"+t)
	}
	_, err := shellGit(root, args...)
	return err
}

func tagAlreadyAt(root, tag, sha string) bool {
	existing, err := shellGit(root, "rev-list", "-n", "1", tag)
	if err != nil {
		return false
	}
	return trim(existing) == sha
}

func trim(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
