package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
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
	showDiffs := fs.Bool("show-diffs", false, "also print unified diffs of proposed go.mod rewrites")
	fs.Parse(args)
	if *since == "" {
		fatal(fmt.Errorf("--since is required"))
	}
	ws, err := workspace.Load(root)
	if err != nil {
		fatal(err)
	}
	plan, err := propagate.NewPlan(ws, *since, "HEAD", propagate.Options{Slug: *slug})
	if err != nil {
		fatal(err)
	}
	printPlan(plan)
	if *showDiffs && len(plan.Entries) > 0 {
		printPlanDiffs(ws, plan)
	}
}

func printPlanDiffs(ws *workspace.Workspace, plan *propagate.Plan) {
	rewrites, err := propagate.ComputeRewrites(ws, plan)
	if err != nil {
		fatal(err)
	}
	if len(rewrites) == 0 {
		return
	}
	fmt.Println()
	// Iterate in plan.Entries order for deterministic output (topo order).
	for _, e := range plan.Entries {
		r, ok := rewrites[e.ModulePath]
		if !ok {
			continue
		}
		display := filepath.Join(ws.Modules[e.ModulePath].RelDir, "go.mod")
		fmt.Print(propagate.UnifiedDiff(display, r.Old, r.New))
	}
}

func cmdPropagateApply(root string, args []string) {
	fs := flag.NewFlagSet("propagate apply", flag.ExitOnError)
	since := fs.String("since", "", "base ref")
	slug := fs.String("slug", "", "train tag slug (default: current branch)")
	remote := fs.String("remote", "origin", "remote to push to; set to \"\" to skip push")
	fs.Parse(args)
	if *since == "" {
		fatal(fmt.Errorf("--since is required"))
	}
	ws, err := workspace.Load(root)
	if err != nil {
		fatal(err)
	}
	plan, err := propagate.NewPlan(ws, *since, "HEAD", propagate.Options{Slug: *slug})
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
