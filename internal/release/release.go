// Package release orchestrates an interactive (or --bump-driven)
// release across a Go workspace: detect affected modules from `replace`
// directives, prompt for per-module bumps, and hand off to propagate.
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
	// Bumps is the pre-supplied per-module bump kind, keyed by module path.
	// Any direct-affected module without an entry will be prompted for
	// (if a Prompter is supplied) or cause a fail-closed error.
	Bumps map[string]bump.Kind
	// PromptCascade, when true, also prompts for cascaded modules.
	PromptCascade bool
	// Slug overrides the train-tag slug; empty = use current branch.
	Slug string
	// Remote is the push target. "" skips push.
	Remote string
}

// Prompter asks the user for a bump kind for one module.
type Prompter interface {
	Ask(modPath, curVersion string, direct bool) (bump.Kind, error)
}

// Plan gathers affected modules, resolves all bumps (via prompter for
// any missing direct-affected entries), builds the propagation plan,
// and writes it to stdout. Returns the plan for optional application.
// A nil plan with nil error means "nothing to release."
func Plan(ws *workspace.Workspace, opts Options, prompter Prompter, stdout io.Writer) (*propagate.Plan, error) {
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

	cascaded := propagate.CascadeExpansion(ws, directs)
	all := append([]string{}, directs...)
	all = append(all, cascaded...)
	sort.Strings(all)

	curVers, err := currentVersions(ws, all)
	if err != nil {
		return nil, err
	}

	directSet := map[string]bool{}
	for _, d := range directs {
		directSet[d] = true
	}

	var toPrompt []string
	for _, m := range all {
		if _, set := bumps[m]; set {
			continue
		}
		if directSet[m] || opts.PromptCascade {
			toPrompt = append(toPrompt, m)
		}
	}
	sort.Strings(toPrompt)

	if len(toPrompt) > 0 {
		if prompter == nil {
			return nil, fmt.Errorf("no bump specified for module(s): %v (supply --bump <module>=<kind>)", toPrompt)
		}
		for _, m := range toPrompt {
			k, err := prompter.Ask(m, curVers[m], directSet[m])
			if err != nil {
				return nil, fmt.Errorf("prompt %s: %w", m, err)
			}
			bumps[m] = k
		}
	}

	plan, err := propagate.NewPlanForModules(ws, directs, propagate.Options{
		Slug:  opts.Slug,
		Bumps: bumps,
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

func currentVersions(ws *workspace.Workspace, modules []string) (map[string]string, error) {
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
