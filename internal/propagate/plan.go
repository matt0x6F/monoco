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

	"github.com/matt0x6f/monoco/internal/bump"
	"github.com/matt0x6f/monoco/internal/gitgraph"
	"github.com/matt0x6f/monoco/internal/workspace"
	"golang.org/x/mod/semver"
)

// Options controls plan construction.
type Options struct {
	// Slug is the human-readable train-tag suffix (e.g. branch name).
	// If empty, NewPlanForModules derives one from the current branch.
	Slug string
	// Today overrides the date used in the train tag; zero means "now".
	Today time.Time
	// Bumps is the per-module bump kind for directly-affected modules.
	// Keyed by module path. Cascaded modules are auto-patched and do
	// NOT need an entry. A Skip entry drops that module from the plan.
	// A direct-affected module without an entry is a fail-closed error.
	Bumps map[string]bump.Kind
	// AllowMajor is the set of module paths permitted to cross a major
	// version boundary in this plan. Keyed by the module's current
	// path (the form in go.mod's `module` line, without any `/vN`
	// suffix). At most one module per plan may cross; see buildPlan.
	AllowMajor map[string]struct{}
	// BaseRef is the remote ref the plan's commit will be pushed to
	// (e.g. "refs/heads/main"). Empty when no push is intended.
	BaseRef string
	// BaseSHA is the SHA of BaseRef on the remote at plan time. Used by
	// Apply to detect concurrent pushes. Empty when BaseRef is empty.
	BaseSHA string
}

// Entry is one module's slice of a plan.
type Entry struct {
	ModulePath   string    // module path (from go.mod)
	RelDir       string    // repo-relative dir (used for tag prefix)
	OldVersion   string    // "" if never tagged
	NewVersion   string    // always set
	Kind         bump.Kind // bump kind applied
	TagName      string    // <RelDir>/<NewVersion>
	DirectChange bool      // true if affected by a replace; false if cascaded
	MajorBump    bool      // true if this entry crosses a major version boundary
}

// Plan is a deterministic propagation plan.
type Plan struct {
	Root      string
	Entries   []Entry
	TrainTag  string // train/<YYYY-MM-DD>-<slug>
	CommitMsg string // "release: <TrainTag>"
	// BaseRef is the remote ref the release will push to (e.g.
	// "refs/heads/main"). Empty when no push is intended.
	BaseRef string
	// BaseSHA is the SHA of BaseRef on the remote captured at plan time.
	// Apply re-checks this before any mutation to detect concurrent
	// pushes; empty disables the check.
	BaseSHA string
}

// NewPlanForModules computes a plan from an explicit directly-affected
// module list. Each listed module is treated as a direct change; the
// transitive consumer closure is computed from the workspace graph and
// the consumers ride the cascade as implicit patches.
//
// modulePaths must be module paths already resolved against the
// workspace; use ResolveModuleRef to accept RelDir inputs.
func NewPlanForModules(ws *workspace.Workspace, modulePaths []string, opts Options) (*Plan, error) {
	if len(modulePaths) == 0 {
		return nil, fmt.Errorf("no modules specified")
	}
	directSet := map[string]struct{}{}
	for _, mp := range modulePaths {
		if _, ok := ws.Modules[mp]; !ok {
			known := make([]string, 0, len(ws.Modules))
			for p := range ws.Modules {
				known = append(known, p)
			}
			sort.Strings(known)
			return nil, fmt.Errorf("module %q not found in workspace (known: %s)", mp, strings.Join(known, ", "))
		}
		directSet[mp] = struct{}{}
	}
	// Transitive closure under reverse-dep edges.
	ordered := transitiveClosure(ws, modulePaths)
	return buildPlan(ws, ordered, directSet, opts)
}

// ResolveModuleRef accepts either a module path (as in go.mod) or a
// RelDir (as in go.work `use`) and returns the canonical module path.
func ResolveModuleRef(ws *workspace.Workspace, input string) (string, bool) {
	if _, ok := ws.Modules[input]; ok {
		return input, true
	}
	want := normalizeRelDir(input)
	for path, mod := range ws.Modules {
		if normalizeRelDir(mod.RelDir) == want {
			return path, true
		}
	}
	return "", false
}

// CascadeExpansion returns the transitive consumer closure of directs,
// minus directs themselves. Exposed so the release orchestrator can show
// which modules will be auto-patched (and optionally prompt for them).
// Returned in deterministic topo order.
func CascadeExpansion(ws *workspace.Workspace, directs []string) []string {
	all := transitiveClosure(ws, directs)
	directSet := map[string]struct{}{}
	for _, d := range directs {
		directSet[d] = struct{}{}
	}
	out := make([]string, 0, len(all))
	for _, m := range all {
		if _, d := directSet[m]; !d {
			out = append(out, m)
		}
	}
	return out
}

// buildPlan constructs entries for the given module set, using
// opts.Bumps for direct-affected kinds and auto-patch for cascades.
func buildPlan(ws *workspace.Workspace, modules []string, directSet map[string]struct{}, opts Options) (*Plan, error) {
	if len(modules) == 0 {
		return &Plan{Root: ws.Root, TrainTag: ""}, nil
	}

	// Classify bumps per module. Fail closed on missing direct entries.
	bumps := map[string]bump.Kind{}
	oldVersions := map[string]string{}
	skipped := map[string]struct{}{}
	var missing []string
	for _, modPath := range modules {
		mod := ws.Modules[modPath]
		rel := normalizeRelDir(mod.RelDir)
		old, err := gitgraph.LatestTagForModule(ws.Root, rel)
		if err != nil {
			return nil, fmt.Errorf("latest tag for %s: %w", modPath, err)
		}
		oldVersions[modPath] = old

		_, isDirect := directSet[modPath]
		if isDirect {
			k, ok := opts.Bumps[modPath]
			if !ok {
				missing = append(missing, modPath)
				continue
			}
			if k == bump.Skip {
				skipped[modPath] = struct{}{}
				continue
			}
			bumps[modPath] = k
			continue
		}
		// Cascaded: auto-patch unless caller overrode with Skip.
		if k, ok := opts.Bumps[modPath]; ok {
			if k == bump.Skip {
				skipped[modPath] = struct{}{}
				continue
			}
			bumps[modPath] = k
			continue
		}
		bumps[modPath] = bump.Patch
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("no bump specified for direct-affected module(s): %s", strings.Join(missing, ", "))
	}

	// Compute new versions. Major-boundary crossings are gated by
	// opts.AllowMajor, and at most one per plan is permitted (keeps
	// import-path rewrite scope bounded).
	versions := map[string]string{}
	majorBumps := map[string]bool{}
	var majorBumpers []string
	for modPath, kind := range bumps {
		old := oldVersions[modPath]
		newV, err := bump.NextVersion(old, kind)
		if err != nil {
			return nil, fmt.Errorf("next version for %s: %w", modPath, err)
		}
		if old != "" && semver.Major(newV) != semver.Major(old) {
			if _, ok := opts.AllowMajor[modPath]; !ok {
				return nil, fmt.Errorf("module %s would cross major version boundary (%s → %s); pass --allow-major %s or set allow_major in monoco.yaml", modPath, old, newV, modPath)
			}
			majorBumps[modPath] = true
			majorBumpers = append(majorBumpers, modPath)
		}
		versions[modPath] = newV
	}
	if len(majorBumpers) > 1 {
		sort.Strings(majorBumpers)
		return nil, fmt.Errorf("at most one module per propagation may cross a major boundary; got: %s", strings.Join(majorBumpers, ", "))
	}

	// Filter skipped modules out of the module set, then topo-order.
	active := modules[:0:0]
	for _, m := range modules {
		if _, isSkip := skipped[m]; isSkip {
			continue
		}
		active = append(active, m)
	}
	ordered := topoOrder(ws, active)

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
			MajorBump:    majorBumps[modPath],
		})
	}

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
		slug = "release"
	}
	train := fmt.Sprintf("train/%s-%s", now.Format("2006-01-02"), slug)

	return &Plan{
		Root:      ws.Root,
		Entries:   entries,
		TrainTag:  train,
		CommitMsg: "release: " + train,
		BaseRef:   opts.BaseRef,
		BaseSHA:   opts.BaseSHA,
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

// transitiveClosure returns the reverse-dep closure of seeds under ws,
// deduplicated, sorted.
func transitiveClosure(ws *workspace.Workspace, seeds []string) []string {
	seen := map[string]struct{}{}
	var stack []string
	for _, s := range seeds {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		stack = append(stack, s)
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, c := range ws.Consumers(cur) {
			if _, ok := seen[c]; ok {
				continue
			}
			seen[c] = struct{}{}
			stack = append(stack, c)
		}
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// topoOrder returns module paths in a deterministic topological order:
// if A requires B (B in workspace), B precedes A.
func topoOrder(ws *workspace.Workspace, modules []string) []string {
	inSet := map[string]bool{}
	for _, m := range modules {
		inSet[m] = true
	}
	inDegree := map[string]int{}
	edges := map[string][]string{}
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

// CurrentBranch returns the short name of the branch currently checked
// out at root (e.g. "main"). Errors if HEAD is detached.
func CurrentBranch(root string) (string, error) {
	return currentBranch(root)
}

func currentBranch(root string) (string, error) {
	out, err := shellGit(root, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// GetRemoteRefSHA returns the SHA of ref on remote via `git ls-remote`.
// ref is a full ref (e.g. "refs/heads/main"). Returns an error if the
// remote has no such ref.
func GetRemoteRefSHA(root, remote, ref string) (string, error) {
	out, err := shellGit(root, "ls-remote", remote, ref)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(out)
	if line == "" {
		return "", fmt.Errorf("remote %q has no ref %q", remote, ref)
	}
	// First line, first column.
	if nl := strings.IndexByte(line, '\n'); nl >= 0 {
		line = line[:nl]
	}
	tab := strings.IndexAny(line, " \t")
	if tab < 0 {
		return "", fmt.Errorf("unexpected ls-remote output: %q", line)
	}
	return strings.TrimSpace(line[:tab]), nil
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
