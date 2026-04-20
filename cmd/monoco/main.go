package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/matt0x6f/monoco/internal/affected"
	"github.com/matt0x6f/monoco/internal/config"
	"github.com/matt0x6f/monoco/internal/gitgraph"
	"github.com/matt0x6f/monoco/internal/tasks"
	"github.com/matt0x6f/monoco/internal/workspace"
)

const usage = `monoco — Go monorepo propagation tool

Usage: monoco <command> [flags]

Graph commands:
  init                     Generate go.work from existing go.mod files.
  sync                     Re-generate go.work after module layout changes.
  affected --since <ref>   Print the affected module set for a commit range.

Task commands (fan out over affected set):
  test     --since <ref>   Run ` + "`go test ./...`" + ` in each affected module.
  lint     --since <ref>   Run ` + "`golangci-lint run`" + ` in each affected module.
  build    --since <ref>   Run ` + "`go build ./...`" + ` in each affected module.
  generate --since <ref>   Run ` + "`go generate ./...`" + ` in each affected module.

Release:
  release                  Cut a release.
                           Affected modules = those with workspace-local
                           ` + "`replace`" + ` directives + transitive consumers.
                           Every module defaults to a patch bump; override
                           with --bump <module>=<kind>.

Run "monoco <command> -h" for command-specific flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	switch cmd {
	case "init":
		cmdInit(root, args)
	case "sync":
		cmdSync(root, args)
	case "affected":
		cmdAffected(root, args)
	case "test":
		cmdTask(root, args, "test", []string{"go", "test", "./..."})
	case "lint":
		cmdTask(root, args, "lint", []string{"golangci-lint", "run"})
	case "build":
		cmdTask(root, args, "build", []string{"go", "build", "./..."})
	case "generate":
		cmdTask(root, args, "generate", []string{"go", "generate", "./..."})
	case "release":
		cmdRelease(root, args)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "monoco:", err)
	os.Exit(1)
}

// cmdInit walks the repo for go.mod files and writes go.work. It also
// drops a stub monoco.yaml alongside, if none exists yet.
func cmdInit(root string, args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	fs.Parse(args)
	dirs, err := discoverModules(root)
	if err != nil {
		fatal(err)
	}
	if err := writeGoWork(root, dirs); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote go.work with %d module(s)\n", len(dirs))
	wrote, err := writeManifestStubIfMissing(root)
	if err != nil {
		fatal(err)
	}
	if wrote {
		fmt.Printf("wrote stub %s\n", config.Filename)
	}
}

const manifestStub = `# monoco.yaml — optional configuration.
# An absent manifest is equivalent to defaults below. Uncomment + edit
# the sections you need and delete the rest; unknown keys are rejected.
version: 1

# Modules excluded from propagation (no tags, no go.mod rewrites) and
# from affected-set / task fanout. Paths match your go.work use entries.
# exclude:
#   - modules/internal-experimental
#   - modules/private-sdk

# Per-task command overrides. Omitted tasks fall back to built-in
# defaults: go test ./...  /  golangci-lint run  /  go build ./...
# /  go generate ./...
# tasks:
#   lint:
#     command: ["golangci-lint", "run", "--timeout=5m"]
`

func writeManifestStubIfMissing(root string) (bool, error) {
	path := filepath.Join(root, config.Filename)
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.WriteFile(path, []byte(manifestStub), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// cmdSync is identical to cmdInit for v1 (idempotent).
func cmdSync(root string, args []string) {
	cmdInit(root, args)
}

// cmdAffected prints affected modules for a commit range.
func cmdAffected(root string, args []string) {
	fs := flag.NewFlagSet("affected", flag.ExitOnError)
	since := fs.String("since", "", "base ref (e.g. main, origin/main, SHA)")
	fs.Parse(args)
	if *since == "" {
		fatal(fmt.Errorf("--since is required"))
	}
	ws, err := workspace.Load(root)
	if err != nil {
		fatal(err)
	}
	mods, err := computeAffectedForRange(ws, *since, "HEAD")
	if err != nil {
		fatal(err)
	}
	for _, m := range mods {
		fmt.Println(m)
	}
}

// discoverModules walks root and returns repo-relative dirs containing go.mod.
// Skips vendor/, .git, and the workspace go.mod at root itself.
func discoverModules(root string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == ".git" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		dir := filepath.Dir(path)
		if dir == root {
			return nil
		}
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			return err
		}
		dirs = append(dirs, rel)
		return nil
	})
	return dirs, err
}

func writeGoWork(root string, dirs []string) error {
	var b strings.Builder
	b.WriteString("go 1.22\n\nuse (\n")
	for _, d := range dirs {
		fmt.Fprintf(&b, "\t./%s\n", d)
	}
	b.WriteString(")\n")
	return os.WriteFile(filepath.Join(root, "go.work"), []byte(b.String()), 0o644)
}

// computeAffectedForRange is a convenience shared by `affected` and task commands.
func computeAffectedForRange(ws *workspace.Workspace, oldRef, newRef string) ([]string, error) {
	files, err := gitgraph.TouchedFiles(ws.Root, oldRef, newRef)
	if err != nil {
		return nil, err
	}
	direct := affected.FromTouchedFiles(ws, files)
	return affected.Compute(ws, direct), nil
}

// cmdTask fans out a command over the affected module set. If
// monoco.yaml overrides the named task's command, that override replaces
// defaultCommand.
func cmdTask(root string, args []string, taskName string, defaultCommand []string) {
	fs := flag.NewFlagSet("task", flag.ExitOnError)
	since := fs.String("since", "", "base ref (e.g. main, origin/main, SHA)")
	all := fs.Bool("all", false, "run against every workspace module, not just affected")
	fs.Parse(args)
	cfg, err := config.Load(root)
	if err != nil {
		fatal(err)
	}
	command := defaultCommand
	if override := cfg.TaskCommand(taskName); override != nil {
		command = override
	}
	ws, err := workspace.LoadWithConfig(root, cfg)
	if err != nil {
		fatal(err)
	}
	var targets []string
	if *all {
		for p := range ws.Modules {
			targets = append(targets, p)
		}
	} else {
		if *since == "" {
			fatal(fmt.Errorf("--since is required (or use --all)"))
		}
		targets, err = computeAffectedForRange(ws, *since, "HEAD")
		if err != nil {
			fatal(err)
		}
	}
	if len(targets) == 0 {
		fmt.Println("(no affected modules)")
		return
	}
	results := tasks.Run(ws, targets, command)
	for _, r := range results {
		status := "ok"
		if r.Err != nil {
			status = "FAIL"
		}
		fmt.Printf("=== %s [%s] ===\n", r.Module, status)
		if len(r.Output) > 0 {
			os.Stdout.Write(r.Output)
			if r.Output[len(r.Output)-1] != '\n' {
				fmt.Println()
			}
		}
	}
	if tasks.AnyFailed(results) {
		os.Exit(1)
	}
}
