package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/matt0x6f/monoco/internal/bump"
	"github.com/matt0x6f/monoco/internal/propagate"
	"github.com/matt0x6f/monoco/internal/release"
	"github.com/matt0x6f/monoco/internal/workspace"
)

// bumpFlag implements flag.Value so --bump can be passed multiple times.
type bumpFlag struct {
	entries []string
}

func (b *bumpFlag) String() string     { return strings.Join(b.entries, ",") }
func (b *bumpFlag) Set(v string) error { b.entries = append(b.entries, v); return nil }

func cmdRelease(root string, args []string) {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	var bumps bumpFlag
	fs.Var(&bumps, "bump", "<module>=<major|minor|patch|skip> (repeatable) — non-interactive bumps")
	promptCascade := fs.Bool("prompt-cascade", false, "prompt for cascaded modules too (default: auto-patch)")
	remote := fs.String("remote", "origin", "remote to push to; set to \"\" to skip push")
	slug := fs.String("slug", "", "train-tag slug (default: current branch)")
	dryRun := fs.Bool("dry-run", false, "print plan and exit")
	assumeYes := fs.Bool("y", false, "skip interactive Proceed? confirmation")
	fs.Parse(args)

	ws, err := workspace.Load(root)
	if err != nil {
		fatal(err)
	}
	bumpMap, err := parseBumpFlags(ws, bumps.entries)
	if err != nil {
		fatal(err)
	}

	var prompter release.Prompter
	if isInteractive() {
		prompter = release.NewStdioPrompter(os.Stdin, os.Stdout)
	}

	opts := release.Options{
		Bumps:         bumpMap,
		PromptCascade: *promptCascade,
		Slug:          *slug,
		Remote:        *remote,
	}

	plan, err := release.Plan(ws, opts, prompter, os.Stdout)
	if err != nil {
		fatal(err)
	}
	if plan == nil {
		return
	}
	if *dryRun {
		return
	}

	if !*assumeYes {
		in := bufio.NewReader(os.Stdin)
		ok, err := release.ConfirmProceed(in, os.Stdout)
		if err != nil {
			fatal(fmt.Errorf("read confirmation: %w", err))
		}
		if !ok {
			fmt.Println("aborted.")
			return
		}
	}

	res, err := release.Apply(ws, plan, opts)
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

func parseBumpFlags(ws *workspace.Workspace, entries []string) (map[string]bump.Kind, error) {
	out := map[string]bump.Kind{}
	for _, e := range entries {
		eq := strings.IndexByte(e, '=')
		if eq < 1 || eq == len(e)-1 {
			return nil, fmt.Errorf("--bump %q: want <module>=<kind>", e)
		}
		ref := strings.TrimSpace(e[:eq])
		kindStr := strings.TrimSpace(e[eq+1:])
		mp, ok := propagate.ResolveModuleRef(ws, ref)
		if !ok {
			return nil, fmt.Errorf("--bump %q: module %q not found in workspace", e, ref)
		}
		k, err := bump.Parse(kindStr)
		if err != nil {
			return nil, fmt.Errorf("--bump %q: %w", e, err)
		}
		out[mp] = k
	}
	return out, nil
}

func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
