// Package propagate computes and applies a propagation plan: the atomic
// set of go.mod rewrites + tags that ship a change and its cascade.
package propagate

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/matt0x6f/monoco/internal/affected"
	"github.com/matt0x6f/monoco/internal/convco"
	"github.com/matt0x6f/monoco/internal/gitgraph"
	"github.com/matt0x6f/monoco/internal/workspace"
	"golang.org/x/mod/semver"
)

// Options controls plan construction.
type Options struct {
	// Slug is the human-readable train-tag suffix (e.g., branch name).
	// If empty, NewPlan derives one from the current branch via git.
	Slug string
	// Today overrides the date used in the train tag; zero means "now".
	Today time.Time
	// BumpOverrides lets the caller force a specific bump per module path.
	BumpOverrides map[string]convco.Kind
}

// Entry is one module's slice of a plan.
type Entry struct {
	ModulePath   string      // module path (from go.mod)
	RelDir       string      // repo-relative dir (used for tag prefix)
	OldVersion   string      // "" if never tagged
	NewVersion   string      // always set
	Kind         convco.Kind // bump kind
	TagName      string      // <RelDir>/<NewVersion>
	DirectChange bool        // true if touched by a commit in range; false if cascaded
}

// Plan is a deterministic propagation plan.
type Plan struct {
	Root      string
	OldRef    string // base commit range start
	NewRef    string // base commit range end
	Entries   []Entry
	TrainTag  string // train/<YYYY-MM-DD>-<slug>
	CommitMsg string // "release: <TrainTag>"
}

// NewPlan computes the plan for the range (oldRef, newRef].
func NewPlan(ws *workspace.Workspace, oldRef, newRef string, opts Options) (*Plan, error) {
	// Identify touched modules from the diff.
	files, err := gitgraph.TouchedFiles(ws.Root, oldRef, newRef)
	if err != nil {
		return nil, fmt.Errorf("diff range: %w", err)
	}
	direct := affected.FromTouchedFiles(ws, files)
	directSet := map[string]struct{}{}
	for _, m := range direct {
		directSet[m] = struct{}{}
	}

	all := affected.Compute(ws, direct)
	if len(all) == 0 {
		return &Plan{Root: ws.Root, OldRef: oldRef, NewRef: newRef, TrainTag: ""}, nil
	}

	// Classify bumps per module.
	bumps := map[string]convco.Kind{}
	for _, modPath := range all {
		mod := ws.Modules[modPath]
		var kinds []convco.Kind
		if _, isDirect := directSet[modPath]; isDirect {
			commits, err := gitgraph.CommitsInRange(ws.Root, oldRef, newRef, normalizeRelDir(mod.RelDir))
			if err != nil {
				return nil, fmt.Errorf("commits for %s: %w", modPath, err)
			}
			for _, c := range commits {
				kinds = append(kinds, convco.Classify(c.Subject, c.Body))
			}
		}
		b := convco.Aggregate(kinds)
		if override, ok := opts.BumpOverrides[modPath]; ok {
			b = override
		}
		// Non-directly-touched modules that were pulled in transitively get
		// at least a patch bump (they must be re-released because their
		// go.mod will change).
		if _, isDirect := directSet[modPath]; !isDirect && b == convco.None {
			b = convco.Patch
		}
		// Directly touched modules with only chore/docs commits still ride
		// the cascade IF one of their deps was bumped — enforce patch.
		if b == convco.None {
			b = convco.Patch
		}
		bumps[modPath] = b
	}

	// Compute new versions. Reject major v1→v2 crossings for v1 of monoco.
	versions := map[string]string{}
	oldVersions := map[string]string{}
	for modPath, kind := range bumps {
		mod := ws.Modules[modPath]
		old, err := gitgraph.LatestTagForModule(ws.Root, normalizeRelDir(mod.RelDir))
		if err != nil {
			return nil, fmt.Errorf("latest tag for %s: %w", modPath, err)
		}
		oldVersions[modPath] = old
		newV, err := convco.NextVersion(old, kind)
		if err != nil {
			return nil, fmt.Errorf("next version for %s: %w", modPath, err)
		}
		if old != "" && semver.Major(newV) != semver.Major(old) {
			return nil, fmt.Errorf("module %s would cross major version boundary (%s → %s); v2+ path rewrites are not supported in monoco v1", modPath, old, newV)
		}
		versions[modPath] = newV
	}

	// Topological order over workspace reverse-dep edges restricted to
	// the affected set. Kahn-style for determinism.
	ordered := topoOrder(ws, all)

	entries := make([]Entry, 0, len(ordered))
	for _, modPath := range ordered {
		mod := ws.Modules[modPath]
		_, isDirect := directSet[modPath]
		rel := normalizeRelDir(mod.RelDir)
		entries = append(entries, Entry{
			ModulePath:   modPath,
			RelDir:       rel,
			OldVersion:   oldVersions[modPath],
			NewVersion:   versions[modPath],
			Kind:         bumps[modPath],
			TagName:      rel + "/" + versions[modPath],
			DirectChange: isDirect,
		})
	}

	// Train tag.
	now := opts.Today
	if now.IsZero() {
		now = time.Now().UTC()
	}
	slug := opts.Slug
	if slug == "" {
		branch, _ := currentBranch(ws.Root)
		slug = sanitizeSlug(branch)
	}
	if slug == "" {
		slug = "propagate"
	}
	train := fmt.Sprintf("train/%s-%s", now.Format("2006-01-02"), slug)

	return &Plan{
		Root:      ws.Root,
		OldRef:    oldRef,
		NewRef:    newRef,
		Entries:   entries,
		TrainTag:  train,
		CommitMsg: "release: " + train,
	}, nil
}

// ModulePaths returns the entries' module paths in plan (topo) order.
func (p *Plan) ModulePaths() []string {
	out := make([]string, len(p.Entries))
	for i, e := range p.Entries {
		out[i] = e.ModulePath
	}
	return out
}

// topoOrder returns module paths in a deterministic topological order:
// if A requires B (B is in workspace), B precedes A.
func topoOrder(ws *workspace.Workspace, modules []string) []string {
	inSet := map[string]bool{}
	for _, m := range modules {
		inSet[m] = true
	}
	inDegree := map[string]int{}
	edges := map[string][]string{} // B -> [A, ...] where A requires B
	for _, m := range modules {
		inDegree[m] = 0
	}
	for _, m := range modules {
		for _, consumer := range ws.Consumers(m) {
			if !inSet[consumer] {
				continue
			}
			edges[m] = append(edges[m], consumer)
			inDegree[consumer]++
		}
	}
	// Kahn with sorted selection for determinism.
	var out []string
	var ready []string
	for m, d := range inDegree {
		if d == 0 {
			ready = append(ready, m)
		}
	}
	sort.Strings(ready)
	for len(ready) > 0 {
		cur := ready[0]
		ready = ready[1:]
		out = append(out, cur)
		for _, next := range edges[cur] {
			inDegree[next]--
			if inDegree[next] == 0 {
				ready = append(ready, next)
				sort.Strings(ready)
			}
		}
	}
	return out
}

func currentBranch(root string) (string, error) {
	out, err := shellGit(root, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func sanitizeSlug(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// normalizeRelDir strips leading "./" and cleans the path so tag-prefix
// matching works regardless of how the path was written in go.work.
func normalizeRelDir(p string) string {
	p = filepath.ToSlash(filepath.Clean(p))
	p = strings.TrimPrefix(p, "./")
	return p
}

func shellGit(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, stderr.String())
	}
	return stdout.String(), nil
}
