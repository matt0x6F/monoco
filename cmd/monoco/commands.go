package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/matt0x6f/monoco/internal/bump"
	"github.com/matt0x6f/monoco/internal/config"
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

// repeatableFlag is a minimal flag.Value for repeatable string flags.
type repeatableFlag struct{ entries []string }

func (r *repeatableFlag) String() string     { return strings.Join(r.entries, ",") }
func (r *repeatableFlag) Set(v string) error { r.entries = append(r.entries, v); return nil }

func cmdRelease(root string, args []string) {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	var bumps bumpFlag
	fs.Var(&bumps, "bump", "<module>=<major|minor|patch|skip> (repeatable) — override the default patch bump")
	var allowMajor repeatableFlag
	fs.Var(&allowMajor, "allow-major", "<module> (repeatable) — permit this module to cross a major version boundary")
	remote := fs.String("remote", "origin", "remote to push to; set to \"\" to skip push")
	slug := fs.String("slug", "", "train-tag slug (default: current branch)")
	dryRun := fs.Bool("dry-run", false, "print plan and exit")
	assumeYes := fs.Bool("y", false, "skip the Proceed? confirmation")
	fs.Parse(args)

	cfg, err := config.Load(root)
	if err != nil {
		fatal(err)
	}
	ws, err := workspace.LoadWithConfig(root, cfg)
	if err != nil {
		fatal(err)
	}
	bumpMap, err := parseBumpFlags(ws, bumps.entries)
	if err != nil {
		fatal(err)
	}
	allowMajorSet, err := resolveAllowMajor(ws, cfg.AllowMajorSet(), allowMajor.entries)
	if err != nil {
		fatal(err)
	}

	opts := release.Options{
		Bumps:      bumpMap,
		Slug:       *slug,
		Remote:     *remote,
		AllowMajor: allowMajorSet,
	}

	// Dry-run is offline: don't ls-remote for a base SHA we won't use.
	planOpts := opts
	if *dryRun {
		planOpts.Remote = ""
	}
	plan, err := release.Plan(ws, planOpts, os.Stdout)
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

// resolveAllowMajor unions CLI --allow-major entries with the manifest's
// allow_major list, resolves each to its canonical module path, and
// returns the set. An unknown entry is a hard error — typos should not
// silently disable the boundary gate.
func resolveAllowMajor(ws *workspace.Workspace, fromManifest map[string]struct{}, fromFlag []string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	for ref := range fromManifest {
		mp, ok := propagate.ResolveModuleRef(ws, ref)
		if !ok {
			return nil, fmt.Errorf("allow_major %q: module not found in workspace", ref)
		}
		out[mp] = struct{}{}
	}
	for _, ref := range fromFlag {
		mp, ok := propagate.ResolveModuleRef(ws, ref)
		if !ok {
			return nil, fmt.Errorf("--allow-major %q: module not found in workspace", ref)
		}
		out[mp] = struct{}{}
	}
	return out, nil
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
