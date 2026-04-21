package propagate

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/matt0x6f/monoco/internal/propagate/importrewrite"
	"github.com/matt0x6f/monoco/internal/workspace"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
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

	// Preflight: if the plan recorded a remote base SHA, re-check that
	// the remote hasn't advanced. This is the cheap path that aborts
	// BEFORE any rewrite/verify work; the lease on push (step 5) closes
	// the residual TOCTOU window.
	if plan.BaseSHA != "" && opts.Remote != "" {
		cur, err := GetRemoteRefSHA(ws.Root, opts.Remote, plan.BaseRef)
		if err != nil {
			return nil, fmt.Errorf("recheck %s %s: %w", opts.Remote, plan.BaseRef, err)
		}
		if cur != plan.BaseSHA {
			return nil, fmt.Errorf("base moved: %s %s was %s when plan was computed, now %s; re-run 'monoco propagate plan' and retry", opts.Remote, plan.BaseRef, plan.BaseSHA, cur)
		}
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

	// 3. Verify module-mode build. Reload the workspace so the post-
	// rewrite module paths (including any /vN majors) are visible.
	verifyWS, err := workspace.Load(ws.Root)
	if err != nil {
		rollback()
		return nil, fmt.Errorf("reload workspace for verify: %w", err)
	}
	paths := make([]string, 0, len(plan.Entries))
	for _, e := range plan.Entries {
		paths = append(paths, targetPath(e))
	}
	if err := Verify(verifyWS, paths); err != nil {
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
	if err := atomicPush(ws.Root, opts.Remote, branch, createdTags, plan.BaseSHA); err != nil {
		return result, fmt.Errorf("atomic push: %w", err)
	}
	result.Pushed = true
	return result, nil
}

// RewrittenMod describes the proposed rewrite of a single module.
type RewrittenMod struct {
	GoModPath    string                      // absolute path to go.mod
	Old          []byte                      // current bytes on disk
	New          []byte                      // proposed bytes after applying the plan
	SumAdds      []string                    // lines to append to this module's go.sum (may be empty)
	GoSumPath    string                      // absolute path to go.sum (valid iff SumAdds non-empty)
	GoFileEdits  []importrewrite.FileChange  // .go source rewrites for major-version path migrations
	SkippedFiles []importrewrite.SkippedFile // .go files the rewriter skipped (build-tag gated)
}

// targetPath returns the module path as it will appear after applying
// the plan. Major-version bumps append a /vN suffix; other entries are
// unchanged.
func targetPath(e Entry) string {
	if !e.MajorBump {
		return e.ModulePath
	}
	return e.ModulePath + "/" + semver.Major(e.NewVersion)
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
	newVersions := map[string]string{}   // keyed by pre-plan module path
	newPaths := map[string]string{}      // old path -> /vN-suffixed path (major bumpers only)
	targetVersion := map[string]string{} // post-plan path -> new version
	var rewrites []importrewrite.Rewrite
	for _, e := range plan.Entries {
		newVersions[e.ModulePath] = e.NewVersion
		target := targetPath(e)
		targetVersion[target] = e.NewVersion
		if target != e.ModulePath {
			newPaths[e.ModulePath] = target
			rewrites = append(rewrites, importrewrite.Rewrite{
				OldPath: e.ModulePath,
				NewPath: target,
			})
		}
	}

	// Precompute canonical h1: hashes for each released module, keyed
	// by the module's post-plan path (so entries generated from a
	// /vN-rewritten go.mod line up).
	hashes := map[string]ModuleHashes{}
	for _, e := range plan.Entries {
		dir := ws.Modules[e.ModulePath].Dir
		target := targetPath(e)
		h, err := ComputeModuleHashes(dir, target, e.NewVersion)
		if err != nil {
			return nil, fmt.Errorf("hash %s@%s: %w", target, e.NewVersion, err)
		}
		hashes[target] = h
	}

	out := map[string]RewrittenMod{}
	for _, e := range plan.Entries {
		modDir := ws.Modules[e.ModulePath].Dir
		goModPath := filepath.Join(modDir, "go.mod")
		b, err := os.ReadFile(goModPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", goModPath, err)
		}
		mf, err := modfile.Parse(goModPath, b, nil)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", goModPath, err)
		}

		modChanged := false
		referencedDeps := map[string]struct{}{} // post-plan paths

		// Rewrite this module's own `module` directive when it's the
		// major-bumper (adds the /vN suffix).
		if e.MajorBump {
			if err := mf.AddModuleStmt(newPaths[e.ModulePath]); err != nil {
				return nil, fmt.Errorf("set module %s: %w", newPaths[e.ModulePath], err)
			}
			modChanged = true
		}

		// Plan require-line updates without mutating while iterating.
		type reqChange struct{ oldPath, newPath, newVersion string }
		var reqChanges []reqChange
		for _, req := range mf.Require {
			newV, inPlan := newVersions[req.Mod.Path]
			if !inPlan {
				continue
			}
			np := req.Mod.Path
			if nm, ok := newPaths[req.Mod.Path]; ok {
				np = nm
			}
			reqChanges = append(reqChanges, reqChange{req.Mod.Path, np, newV})
		}
		for _, c := range reqChanges {
			if c.oldPath != c.newPath {
				if err := mf.DropRequire(c.oldPath); err != nil {
					return nil, fmt.Errorf("drop require %s: %w", c.oldPath, err)
				}
			}
			if err := mf.AddRequire(c.newPath, c.newVersion); err != nil {
				return nil, fmt.Errorf("add require %s: %w", c.newPath, err)
			}
			referencedDeps[c.newPath] = struct{}{}
			modChanged = true
		}

		// Drop workspace-local replaces for in-plan deps. A replace is
		// "workspace-local" iff its New.Version is empty (path replacement)
		// and resolves to a workspace module's dir.
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
			depV := targetVersion[dep]
			sumAdds = append(sumAdds,
				fmt.Sprintf("%s %s %s", dep, depV, h.H1),
				fmt.Sprintf("%s %s/go.mod %s", dep, depV, h.H1Mod),
			)
		}
		sort.Strings(sumAdds)

		// Rewrite any .go files whose imports reference a major-bumped
		// module (affects this module iff any such bumper exists in
		// the plan). Applied to every entry — including the bumper
		// itself, which may import its own subpackages by full path.
		var goEdits []importrewrite.FileChange
		var skipped []importrewrite.SkippedFile
		if len(rewrites) > 0 {
			rep, rerr := importrewrite.RewriteConsumer(modDir, rewrites)
			if rerr != nil {
				return nil, fmt.Errorf("rewrite imports in %s: %w", modDir, rerr)
			}
			goEdits = rep.Changes
			skipped = rep.SkippedFiles
		}

		if !modChanged && len(sumAdds) == 0 && len(goEdits) == 0 {
			continue
		}

		mf.Cleanup()
		formatted, err := mf.Format()
		if err != nil {
			return nil, fmt.Errorf("format %s: %w", goModPath, err)
		}

		r := RewrittenMod{
			GoModPath:    goModPath,
			Old:          b,
			New:          formatted,
			SumAdds:      sumAdds,
			GoFileEdits:  goEdits,
			SkippedFiles: skipped,
		}
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
		for _, fc := range r.GoFileEdits {
			if err := os.WriteFile(fc.Path, fc.New, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", fc.Path, err)
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

func atomicPush(root, remote, branch string, tags []string, leaseSHA string) error {
	build := func(withLease bool) []string {
		args := []string{"push", "--atomic"}
		if withLease && leaseSHA != "" {
			args = append(args, "--force-with-lease="+branch+":"+leaseSHA)
		}
		args = append(args, remote, branch)
		for _, t := range tags {
			args = append(args, "refs/tags/"+t)
		}
		return args
	}

	if leaseSHA == "" {
		_, err := shellGit(root, build(false)...)
		return err
	}

	// Try with lease first. If the remote's branch-protection config
	// rejects force-style pushes even when the lease holds, fall back
	// to a plain atomic push: a non-fast-forward would still be
	// rejected, and the preflight SHA check already caught the common
	// race. The fallback keeps monoco usable on strict GitHub orgs.
	_, err := shellGit(root, build(true)...)
	if err == nil {
		return nil
	}
	if !isProtectedBranchLeaseReject(err) {
		return err
	}
	_, err2 := shellGit(root, build(false)...)
	return err2
}

// isProtectedBranchLeaseReject reports whether a push failure looks
// like a protected-branch rule blocking force-with-lease. We retry
// without the lease in that case.
func isProtectedBranchLeaseReject(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "protected branch hook declined"):
		return true
	case strings.Contains(msg, "cannot force-push to this protected branch"):
		return true
	case strings.Contains(msg, "force pushes are not allowed"):
		return true
	}
	return false
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
