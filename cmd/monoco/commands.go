package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/matt0x6f/monoco/internal/propagate"
	"github.com/matt0x6f/monoco/internal/workspace"
)

func cmdPropagate(root string, args []string) {
	if len(args) == 0 {
		fatal(fmt.Errorf("propagate: need subcommand (plan|apply)"))
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "plan":
		cmdPropagatePlan(root, rest)
	case "apply":
		cmdPropagateApply(root, rest)
	default:
		fatal(fmt.Errorf("propagate: unknown subcommand %q", sub))
	}
}

func cmdPropagatePlan(root string, args []string) {
	fs := flag.NewFlagSet("propagate plan", flag.ExitOnError)
	since := fs.String("since", "", "base ref")
	slug := fs.String("slug", "", "train tag slug (default: current branch)")
	modules := fs.String("modules", "", "comma-separated module list (RelDir or module path); skips diff-based affected-set")
	fs.Parse(args)
	ws, err := workspace.Load(root)
	if err != nil {
		fatal(err)
	}
	plan, err := buildPropagatePlan(ws, *since, *modules, propagate.Options{Slug: *slug})
	if err != nil {
		fatal(err)
	}
	printPlan(plan)
}

func cmdPropagateApply(root string, args []string) {
	fs := flag.NewFlagSet("propagate apply", flag.ExitOnError)
	since := fs.String("since", "", "base ref")
	slug := fs.String("slug", "", "train tag slug (default: current branch)")
	remote := fs.String("remote", "origin", "remote to push to; set to \"\" to skip push")
	modules := fs.String("modules", "", "comma-separated module list (RelDir or module path); skips diff-based affected-set")
	fs.Parse(args)
	ws, err := workspace.Load(root)
	if err != nil {
		fatal(err)
	}
	plan, err := buildPropagatePlan(ws, *since, *modules, propagate.Options{Slug: *slug})
	if err != nil {
		fatal(err)
	}
	if len(plan.Entries) == 0 {
		fmt.Println("nothing to propagate.")
		return
	}
	fmt.Println("Plan:")
	printPlan(plan)
	res, err := propagate.Apply(ws, plan, propagate.ApplyOptions{Remote: *remote})
	if err != nil {
		fatal(err)
	}
	fmt.Printf("\nRelease commit: %s\n", res.ReleaseCommit)
	fmt.Printf("Tags created: %d\n", len(res.Tags))
	if res.Pushed {
		fmt.Println("Pushed to", *remote)
	} else {
		fmt.Println("Not pushed (remote unset or push skipped).")
	}
}

// buildPropagatePlan dispatches to NewPlan or NewPlanForModules based on which
// of --since / --modules was set, and enforces mutual exclusion.
func buildPropagatePlan(ws *workspace.Workspace, since, modules string, opts propagate.Options) (*propagate.Plan, error) {
	if since != "" && modules != "" {
		return nil, fmt.Errorf("--since and --modules are mutually exclusive; pick one")
	}
	if since == "" && modules == "" {
		return nil, fmt.Errorf("must specify --since or --modules")
	}
	if modules != "" {
		refs, err := splitModulesList(modules)
		if err != nil {
			return nil, err
		}
		paths := make([]string, 0, len(refs))
		for _, r := range refs {
			p, ok := propagate.ResolveModuleRef(ws, r)
			if !ok {
				return nil, fmt.Errorf("module %q not found in workspace", r)
			}
			paths = append(paths, p)
		}
		return propagate.NewPlanForModules(ws, paths, opts)
	}
	return propagate.NewPlan(ws, since, "HEAD", opts)
}

// splitModulesList parses the --modules CSV value, trimming whitespace and
// rejecting empty entries.
func splitModulesList(v string) ([]string, error) {
	raw := strings.Split(v, ",")
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, fmt.Errorf("--modules has empty entry")
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--modules is empty")
	}
	return out, nil
}

func printPlan(plan *propagate.Plan) {
	if len(plan.Entries) == 0 {
		fmt.Println("(no modules affected)")
		return
	}
	fmt.Printf("Train: %s\n\n", plan.TrainTag)
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MODULE\tOLD\tNEW\tKIND\tDIRECT")
	for _, e := range plan.Entries {
		old := e.OldVersion
		if old == "" {
			old = "(none)"
		}
		direct := "cascade"
		if e.DirectChange {
			direct = "direct"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.ModulePath, old, e.NewVersion, e.Kind, direct)
	}
	tw.Flush()
}
