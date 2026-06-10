package propagate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/matt0x6f/monoco/internal/gitx"
	"github.com/matt0x6f/monoco/internal/workspace"
	"golang.org/x/mod/modfile"
)

// Pin records a require edge that a staged release leaves at its
// previously tagged version: the module owning the pin releases before
// Path does, so its go.mod keeps requiring Path@Version.
type Pin struct {
	Path    string // module path of the dependency left at its old version
	Version string // the version the owner's go.mod already requires
}

// stagePlan assigns a release stage to every active module. Acyclic
// plans never reach here; callers handle the all-stage-1 case directly.
//
// Modules that require each other cannot be tagged at the same commit
// (their go.sum hashes would be mutually recursive), but they CAN be
// tagged at successive commits inside one atomic push: the first side
// keeps requiring the other's previous tag. stagePlan finds that order:
//
//  1. Condense the require graph into strongly-connected components and
//     walk the condensation in topo order.
//  2. Singleton components release in the earliest stage their in-plan
//     dependencies allow.
//  3. Multi-member components are peeled one module per stage. A member
//     may peel only if its new content builds in module mode against the
//     *previously tagged* content of the members still unpeeled — that is
//     what external consumers of its new tag will resolve. opts.Cuts
//     forces the first peel; otherwise cascaded members are tried before
//     direct ones so the actively developed module ships last, seeing
//     the freshest content.
//
// If no member of a component can peel, the modules are release-coupled —
// each side's new code needs the other's new API — and no staged order
// exists for any tool; the error says to merge them or break the cycle.
func stagePlan(ws *workspace.Workspace, active []string, directSet map[string]struct{}, cuts map[string]struct{}) (stageOf map[string]int, pins map[string][]Pin, maxStage int, err error) {
	comps := condensationOrder(ws, active)

	inCycle := map[string]bool{}
	for _, c := range comps {
		if len(c) > 1 {
			for _, m := range c {
				inCycle[m] = true
			}
		}
	}
	var badCuts []string
	for cut := range cuts {
		if !inCycle[cut] {
			badCuts = append(badCuts, cut)
		}
	}
	if len(badCuts) > 0 {
		sort.Strings(badCuts)
		return nil, nil, 0, fmt.Errorf("--cut %s: not part of a require cycle in this plan", strings.Join(badCuts, ", "))
	}

	deps := inPlanDeps(ws, active)

	wts := newWorktrees(ws.Root)
	defer wts.cleanup()

	stageOf = map[string]int{}
	pins = map[string][]Pin{}
	maxStage = 1

	for _, comp := range comps {
		// Earliest stage this component may start in: after every
		// in-plan dependency outside the component.
		compSet := map[string]bool{}
		for _, m := range comp {
			compSet[m] = true
		}
		base := 1
		for _, m := range comp {
			for _, d := range deps[m] {
				if !compSet[d] && stageOf[d] > base {
					base = stageOf[d]
				}
			}
		}

		if len(comp) == 1 {
			m := comp[0]
			stageOf[m] = base
			if base > maxStage {
				maxStage = base
			}
			continue
		}

		remaining := append([]string(nil), comp...)
		for i := 0; len(remaining) > 0; i++ {
			candidates := peelOrder(remaining, directSet, cuts, i == 0)
			picked := ""
			var trials []error
			for _, c := range candidates {
				unpeeled := without(remaining, c)
				pinned, candPins, perr := pinDirsFor(ws, wts, c, unpeeled)
				if perr != nil {
					return nil, nil, 0, perr
				}
				if verr := verifyOne(context.Background(), ws.Modules[c].Dir, ws, pinned); verr != nil {
					trials = append(trials, fmt.Errorf("%s released first: %w", c, verr))
					continue
				}
				picked = c
				pins[c] = candPins
				break
			}
			if picked == "" {
				sort.Strings(remaining)
				return nil, nil, 0, fmt.Errorf(
					"modules %s are release-coupled: no staged order compiles — each side's new code requires the other's new API, so no release order works for external consumers. This cycle is one module pretending to be two: merge the modules, or break the require cycle in code.\n%w",
					strings.Join(remaining, ", "), errors.Join(trials...))
			}
			stageOf[picked] = base + i
			if stageOf[picked] > maxStage {
				maxStage = stageOf[picked]
			}
			remaining = without(remaining, picked)
		}
	}
	return stageOf, pins, maxStage, nil
}

// peelOrder ranks candidates for the next peel: a --cut module first
// (only for the opening peel of its component), then cascaded members
// before direct-affected ones, then lexicographic. When a cut is named
// it is the ONLY candidate — a forced cut that doesn't compile should
// fail loudly, not silently fall back to another order.
func peelOrder(remaining []string, directSet, cuts map[string]struct{}, first bool) []string {
	if first {
		for _, m := range remaining {
			if _, ok := cuts[m]; ok {
				return []string{m}
			}
		}
	}
	out := append([]string(nil), remaining...)
	rank := func(m string) int {
		if _, direct := directSet[m]; direct {
			return 1
		}
		return 0
	}
	sort.Slice(out, func(i, j int) bool {
		if rank(out[i]) != rank(out[j]) {
			return rank(out[i]) < rank(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// pinDirsFor materializes, for candidate c, every unpeeled cycle member
// it directly requires, at the version c's go.mod already pins. Returns
// the replace-override map for verifyOne and the Pin records for the
// plan. A pinned version that isn't a tag is a hard error: there is no
// previously released content to stage against.
func pinDirsFor(ws *workspace.Workspace, wts *worktrees, c string, unpeeled []string) (map[string]string, []Pin, error) {
	modDir := ws.Modules[c].Dir
	pinned := map[string]string{}
	var pins []Pin
	for _, d := range unpeeled {
		ver, err := requireVersion(modDir, d)
		if err != nil {
			return nil, nil, err
		}
		if ver == "" {
			// c doesn't require d directly (cycle via a third member).
			continue
		}
		rel := normalizeRelDir(ws.Modules[d].RelDir)
		tag := rel + "/" + ver
		root, err := wts.dirFor(tag)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot stage require cycle: %s requires %s@%s, which is not a tag in this repository — staged releases pin previously released versions. Tag it first, or break the cycle in code. (%v)", c, d, ver, err)
		}
		pinned[d] = filepath.Join(root, filepath.FromSlash(rel))
		pins = append(pins, Pin{Path: d, Version: ver})
	}
	sort.Slice(pins, func(i, j int) bool { return pins[i].Path < pins[j].Path })
	return pinned, pins, nil
}

// requireVersion returns the version modDir's go.mod requires for
// depPath, or "" if there is no such require.
func requireVersion(modDir, depPath string) (string, error) {
	goModPath := filepath.Join(modDir, "go.mod")
	b, err := os.ReadFile(goModPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", goModPath, err)
	}
	mf, err := modfile.Parse(goModPath, b, nil)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", goModPath, err)
	}
	for _, req := range mf.Require {
		if req.Mod.Path == depPath {
			return req.Mod.Version, nil
		}
	}
	return "", nil
}

// inPlanDeps returns, for each module, the in-plan modules it directly
// requires (the dependency direction; ws.Consumers is the reverse).
func inPlanDeps(ws *workspace.Workspace, modules []string) map[string][]string {
	inSet := map[string]bool{}
	for _, m := range modules {
		inSet[m] = true
	}
	deps := map[string][]string{}
	for _, d := range modules {
		for _, c := range ws.Consumers(d) {
			if inSet[c] {
				deps[c] = append(deps[c], d)
			}
		}
	}
	return deps
}

// condensationOrder returns the strongly-connected components of the
// in-plan require graph, in topological order of the condensation
// (a component appears after every component it depends on). Members
// within a component are sorted; the order across equal-rank components
// is deterministic.
func condensationOrder(ws *workspace.Workspace, modules []string) [][]string {
	deps := inPlanDeps(ws, modules)
	sorted := append([]string(nil), modules...)
	sort.Strings(sorted)

	// Tarjan's algorithm (recursive; plans are small).
	index := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	next := 0
	var comps [][]string

	var strongconnect func(v string)
	strongconnect = func(v string) {
		index[v] = next
		low[v] = next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range deps[v] {
			if _, seen := index[w]; !seen {
				strongconnect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] == index[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			sort.Strings(comp)
			comps = append(comps, comp)
		}
	}
	for _, m := range sorted {
		if _, seen := index[m]; !seen {
			strongconnect(m)
		}
	}
	// Tarjan emits components dependencies-first when edges point
	// dep → ... here deps[v] points v → its dependencies, so a component
	// is emitted only after everything it depends on. That is already
	// condensation topo order.
	return comps
}

func without(s []string, drop string) []string {
	out := make([]string, 0, len(s))
	for _, v := range s {
		if v != drop {
			out = append(out, v)
		}
	}
	return out
}

// worktrees materializes repository content at tags into detached git
// worktrees, one per tag, cached for the lifetime of a plan or apply.
type worktrees struct {
	root string
	dirs map[string]string // tag -> worktree root
	tmps []string          // parent temp dirs to remove on cleanup
}

func newWorktrees(root string) *worktrees {
	return &worktrees{root: root, dirs: map[string]string{}}
}

func (w *worktrees) dirFor(tag string) (string, error) {
	if d, ok := w.dirs[tag]; ok {
		return d, nil
	}
	if _, err := gitx.Run(context.Background(), w.root, "rev-parse", "--verify", "refs/tags/"+tag); err != nil {
		return "", fmt.Errorf("tag %s: %w", tag, err)
	}
	tmp, err := os.MkdirTemp("", "monoco-pin-*")
	if err != nil {
		return "", err
	}
	target := filepath.Join(tmp, "wt")
	if _, err := gitx.Run(context.Background(), w.root, "worktree", "add", "--detach", target, "refs/tags/"+tag); err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("materialize %s: %w", tag, err)
	}
	w.dirs[tag] = target
	w.tmps = append(w.tmps, tmp)
	return target, nil
}

func (w *worktrees) cleanup() {
	for _, d := range w.dirs {
		_, _ = gitx.Run(context.Background(), w.root, "worktree", "remove", "--force", d)
	}
	for _, t := range w.tmps {
		_ = os.RemoveAll(t)
	}
	w.dirs = map[string]string{}
	w.tmps = nil
}
