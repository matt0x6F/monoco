// Package release orchestrates a release across a Go workspace:
// detect affected modules from `replace` directives, apply per-module
// bump kinds (default: patch; override via Options.Bumps), and hand
// off to propagate for rewrite/tag/push.
package release

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/matt0x6f/monoco/internal/bump"
	"github.com/matt0x6f/monoco/internal/gitgraph"
	"github.com/matt0x6f/monoco/internal/propagate"
	"github.com/matt0x6f/monoco/internal/workspace"
)

// Options configures a release run.
type Options struct {
	// Bumps is the per-module bump kind override, keyed by module path.
	// Any module not in this map defaults to bump.Patch.
	// A bump.Skip entry drops that module from the plan.
	Bumps map[string]bump.Kind
	// Slug overrides the train-tag slug; empty = use current branch.
	Slug string
	// Remote is the push target. "" skips push.
	Remote string
	// AllowMajor is the set of module paths permitted to cross a major
	// version boundary in this release. Typically unioned from the
	// --allow-major CLI flag and monoco.yaml's allow_major entries.
	AllowMajor map[string]struct{}
}

// Plan gathers affected modules, applies bump kinds (default patch,
// override via opts.Bumps), builds the propagation plan, and writes it
// to stdout. A nil plan with nil error means "nothing to release."
func Plan(ws *workspace.Workspace, opts Options, stdout io.Writer) (*propagate.Plan, error) {
	directs, err := propagate.DirectFromReplaces(ws)
	if err != nil {
		return nil, fmt.Errorf("detect affected modules: %w", err)
	}
	if len(directs) == 0 {
		fmt.Fprintln(stdout, "no modules have workspace-local `replace` directives; nothing to release.")
		return nil, nil
	}

	bumps := map[string]bump.Kind{}
	for k, v := range opts.Bumps {
		bumps[k] = v
	}
	// Direct-affected modules default to Patch unless overridden.
	for _, d := range directs {
		if _, set := bumps[d]; !set {
			bumps[d] = bump.Patch
		}
	}

	plan, err := propagate.NewPlanForModules(ws, directs, propagate.Options{
		Slug:       opts.Slug,
		Bumps:      bumps,
		AllowMajor: opts.AllowMajor,
	})
	if err != nil {
		return nil, err
	}
	if len(plan.Entries) == 0 {
		fmt.Fprintln(stdout, "plan is empty after Skip filtering; nothing to release.")
		return nil, nil
	}
	printPlan(stdout, plan)
	return plan, nil
}

// Apply executes the plan. Thin wrapper around propagate.Apply so the
// CLI only imports `release`.
func Apply(ws *workspace.Workspace, plan *propagate.Plan, opts Options) (*propagate.ApplyResult, error) {
	return propagate.Apply(ws, plan, propagate.ApplyOptions{Remote: opts.Remote})
}

// CurrentVersions reports the latest tagged version per module path.
// Exposed mainly so CLIs can show pre-release context.
func CurrentVersions(ws *workspace.Workspace, modules []string) (map[string]string, error) {
	sort.Strings(modules)
	out := map[string]string{}
	for _, mp := range modules {
		m := ws.Modules[mp]
		rel := filepath.ToSlash(filepath.Clean(m.RelDir))
		v, err := latestTag(ws.Root, rel)
		if err != nil {
			return nil, err
		}
		out[mp] = v
	}
	return out, nil
}

// latestTag is a seam for tests.
var latestTag = gitgraph.LatestTagForModule

func printPlan(w io.Writer, p *propagate.Plan) {
	fmt.Fprintf(w, "\nPlan:\n  Train: %s\n\n", p.TrainTag)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  MODULE\tOLD\tNEW\tKIND\tDIRECT")
	for _, e := range p.Entries {
		old := e.OldVersion
		if old == "" {
			old = "(none)"
		}
		direct := "cascade"
		if e.DirectChange {
			direct = "direct"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", e.ModulePath, old, e.NewVersion, e.Kind, direct)
	}
	tw.Flush()
	fmt.Fprintln(w)
}
