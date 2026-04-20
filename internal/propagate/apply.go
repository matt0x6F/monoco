package propagate

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// Apply executes plan against ws. The working tree must be clean. On any
// pre-push failure (preflight, rewrite, verify, commit, tag) the
// working tree and refs are restored to the pre-run state. Push failures
// leave the local commit and tags intact for retry.
func Apply(ws *workspace.Workspace, plan *Plan, opts ApplyOptions) (*ApplyResult, error) {
	if len(plan.Entries) == 0 {
		return nil, fmt.Errorf("empty plan")
	}

	// Preflight: working tree must be clean.
	if err := requireCleanWorkingTree(ws.Root); err != nil {
		return nil, err
	}

	// Snapshot HEAD for rollback.
	oldHeadOut, err := shellGit(ws.Root, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	oldHead := trim(oldHeadOut)

	// Track tags we create locally so we can roll them back.
	var createdTags []string
	rollback := func() {
		for _, t := range createdTags {
			_, _ = shellGit(ws.Root, "tag", "-d", t)
		}
		// reset --hard to oldHead: discards release commit and restores working tree.
		_, _ = shellGit(ws.Root, "reset", "--hard", oldHead)
	}

	// 1. Rewrite go.mod + go.sum in topo order.
	if err := rewriteGoMods(ws, plan); err != nil {
		rollback()
		return nil, fmt.Errorf("rewrite go.mods: %w", err)
	}

	// 2. Create the release commit.
	if _, err := shellGit(ws.Root, "add", "-A"); err != nil {
		rollback()
		return nil, err
	}
	if _, err := shellGit(ws.Root, "commit", "-m", plan.CommitMsg); err != nil {
		rollback()
		return nil, fmt.Errorf("create release commit: %w", err)
	}

	// 3. Verify module-mode build.
	paths := plan.ModulePaths()
	if err := Verify(ws, paths); err != nil {
		rollback()
		return nil, fmt.Errorf("verify: %w", err)
	}

	releaseSHAOut, err := shellGit(ws.Root, "rev-parse", "HEAD")
	if err != nil {
		rollback()
		return nil, err
	}
	releaseSHA := trim(releaseSHAOut)

	// 4. Create tags.
	for _, e := range plan.Entries {
		if tagAlreadyAt(ws.Root, e.TagName, releaseSHA) {
			createdTags = append(createdTags, e.TagName)
			continue
		}
		if _, err := shellGit(ws.Root, "tag", e.TagName, releaseSHA); err != nil {
			rollback()
			return nil, fmt.Errorf("tag %s: %w", e.TagName, err)
		}
		createdTags = append(createdTags, e.TagName)
	}
	if !tagAlreadyAt(ws.Root, plan.TrainTag, releaseSHA) {
		if _, err := shellGit(ws.Root, "tag", plan.TrainTag, releaseSHA); err != nil {
			rollback()
			return nil, fmt.Errorf("tag %s: %w", plan.TrainTag, err)
		}
	}
	createdTags = append(createdTags, plan.TrainTag)

	result := &ApplyResult{ReleaseCommit: releaseSHA, Tags: createdTags}

	// 5. Atomic push (optional). After this point, failure does NOT roll
	// back: local tags + commit are valid; the engineer can retry push.
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
	if err := atomicPush(ws.Root, opts.Remote, branch, createdTags); err != nil {
		return result, fmt.Errorf("atomic push: %w", err)
	}
	result.Pushed = true
	return result, nil
}

// RewrittenMod describes the proposed rewrite of a single module.
type RewrittenMod struct {
	GoModPath string            // absolute path to go.mod
	Old       []byte            // current bytes on disk
	New       []byte            // proposed bytes after applying the plan
	SumAdds   []string          // lines to append to this module's go.sum (may be empty)
	GoSumPath string            // absolute path to go.sum (valid iff SumAdds non-empty)
}

// ComputeRewrites returns the proposed go.mod rewrites + go.sum additions
// for plan, in memory. It does not touch the filesystem. Both Apply and
// dry-run output consume this so the preview is byte-identical.
//
// Rewrites include: bumping `require` versions for any dep also in the
// plan, and dropping workspace-local `replace` directives for deps also
// in the plan. go.sum entries are computed for every freshly-tagged
// module that any downstream in the plan depends on.
func ComputeRewrites(ws *workspace.Workspace, plan *Plan) (map[string]RewrittenMod, error) {
	newVersions := map[string]string{}
	for _, e := range plan.Entries {
		newVersions[e.ModulePath] = e.NewVersion
	}

	// Precompute canonical h1: hashes for each released module. The
	// hash is over the module's current workspace contents — which, at
	// release time, IS what will get tagged (we tag HEAD after rewrite).
	hashes := map[string]ModuleHashes{}
	for _, e := range plan.Entries {
		dir := ws.Modules[e.ModulePath].Dir
		h, err := ComputeModuleHashes(dir, e.ModulePath, e.NewVersion)
		if err != nil {
			return nil, fmt.Errorf("hash %s@%s: %w", e.ModulePath, e.NewVersion, err)
		}
		hashes[e.ModulePath] = h
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

		modChanged := false
		referencedDeps := map[string]struct{}{}

		// Bump require versions for in-plan deps.
		for _, req := range mf.Require {
			if newV, inPlan := newVersions[req.Mod.Path]; inPlan {
				if err := mf.AddRequire(req.Mod.Path, newV); err != nil {
					return nil, fmt.Errorf("update require %s: %w", req.Mod.Path, err)
				}
				referencedDeps[req.Mod.Path] = struct{}{}
				modChanged = true
			}
		}

		// Drop workspace-local replaces for in-plan deps. A replace is
		// "workspace-local" iff its New.Version is empty (path replacement)
		// and resolves to a workspace module's dir.
		modDir := ws.Modules[e.ModulePath].Dir
		for _, r := range mf.Replace {
			if r.New.Version != "" {
				continue
			}
			if !isLocalPath(r.New.Path) {
				continue
			}
			target := filepath.Clean(filepath.Join(modDir, r.New.Path))
			found := false
			for _, m := range ws.Modules {
				absDir, _ := filepath.Abs(m.Dir)
				if filepath.Clean(absDir) == target {
					if _, inPlan := newVersions[m.Path]; inPlan {
						found = true
					}
					break
				}
			}
			if !found {
				continue
			}
			if err := mf.DropReplace(r.Old.Path, r.Old.Version); err != nil {
				return nil, fmt.Errorf("drop replace %s: %w", r.Old.Path, err)
			}
			modChanged = true
		}

		// Always compute sum additions for deps this module references
		// (caught via Require above). Cascaded modules may have no
		// in-plan requires — skip them.
		var sumAdds []string
		for dep := range referencedDeps {
			h := hashes[dep]
			depV := newVersions[dep]
			sumAdds = append(sumAdds,
				fmt.Sprintf("%s %s %s", dep, depV, h.H1),
				fmt.Sprintf("%s %s/go.mod %s", dep, depV, h.H1Mod),
			)
		}
		sort.Strings(sumAdds)

		if !modChanged && len(sumAdds) == 0 {
			continue
		}

		mf.Cleanup()
		formatted, err := mf.Format()
		if err != nil {
			return nil, fmt.Errorf("format %s: %w", goModPath, err)
		}

		r := RewrittenMod{GoModPath: goModPath, Old: b, New: formatted, SumAdds: sumAdds}
		if len(sumAdds) > 0 {
			r.GoSumPath = filepath.Join(ws.Modules[e.ModulePath].Dir, "go.sum")
		}
		out[e.ModulePath] = r
	}
	return out, nil
}

// rewriteGoMods writes the computed rewrites to disk, including go.sum
// additions for freshly-pinned deps.
func rewriteGoMods(ws *workspace.Workspace, plan *Plan) error {
	rewrites, err := ComputeRewrites(ws, plan)
	if err != nil {
		return err
	}
	for _, r := range rewrites {
		if !bytes.Equal(r.Old, r.New) {
			if err := os.WriteFile(r.GoModPath, r.New, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", r.GoModPath, err)
			}
		}
		if len(r.SumAdds) == 0 {
			continue
		}
		if err := mergeGoSum(r.GoSumPath, r.SumAdds); err != nil {
			return fmt.Errorf("merge %s: %w", r.GoSumPath, err)
		}
	}
	return nil
}

// mergeGoSum appends lines to a go.sum, deduplicating with any existing
// content, and keeps the file sorted.
func mergeGoSum(goSumPath string, adds []string) error {
	var existing [][]byte
	if b, err := os.ReadFile(goSumPath); err == nil {
		for _, line := range bytes.Split(b, []byte("\n")) {
			if len(line) == 0 {
				continue
			}
			existing = append(existing, line)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	seen := map[string]struct{}{}
	for _, l := range existing {
		seen[string(l)] = struct{}{}
	}
	for _, a := range adds {
		if _, dup := seen[a]; dup {
			continue
		}
		seen[a] = struct{}{}
		existing = append(existing, []byte(a))
	}
	sort.Slice(existing, func(i, j int) bool {
		return bytes.Compare(existing[i], existing[j]) < 0
	})
	var buf bytes.Buffer
	for _, l := range existing {
		buf.Write(l)
		buf.WriteByte('\n')
	}
	return os.WriteFile(goSumPath, buf.Bytes(), 0o644)
}

func requireCleanWorkingTree(root string) error {
	out, err := shellGit(root, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(out) != "" {
		return fmt.Errorf("working tree is not clean:\n%s", out)
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
