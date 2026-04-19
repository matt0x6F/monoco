// POC-1: compute affected module sets from go.work + per-module go.mod.
//
// Answers: can we build a reverse-dependency graph across a Go monorepo
// without parsing source, and compute transitive affected sets fast enough?
//
// Approach notes
// --------------
// The original plan called for `go list -m -json all` per module (with
// GOWORK=off) to enumerate deps. In the fixture generator we use a sentinel
// pseudo-version (v0.0.0-00010101000000-000000000000) on every cross-module
// require, and `go list -m all` refuses to resolve that version even with
// workspace mode on, so it exits non-zero before producing any output.
//
// Since workspace modules are already named in go.work and their direct deps
// are enumerated by each module's go.mod require block, we get the same edges
// by parsing go.mod with golang.org/x/mod/modfile. That's still "no source
// parsing" — the go.mod file is declarative metadata — and it validates the
// core claim: we can build a correct workspace reverse-dep graph without
// reading any .go files.
//
// For situations where a module's real deps exceed what's in its go.mod
// (e.g. go-list resolves indirects via MVS), we also support the go-list
// path via the -go-list flag on the CLI; the test exercises the go.mod path
// because that is what the fixture's pseudo-versions allow.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

// Graph is a module-path -> set-of-module-paths reverse-dep map
// restricted to modules inside the workspace.
type Graph struct {
	// WorkspaceModules maps module path -> on-disk dir.
	WorkspaceModules map[string]string
	// ReverseDeps[A] = set of modules in the workspace that require A.
	ReverseDeps map[string]map[string]struct{}
}

// BuildGraph parses <root>/go.work, reads each workspace module's go.mod to
// enumerate its required modules, and records reverse-dep edges where both
// endpoints are workspace modules.
func BuildGraph(root string) (*Graph, error) {
	return buildGraph(root, false)
}

// BuildGraphWithGoList is like BuildGraph but shells out to
// `go list -m -json all` per module instead of parsing go.mod. Requires that
// every require entry can be resolved (no sentinel pseudo-versions).
func BuildGraphWithGoList(root string) (*Graph, error) {
	return buildGraph(root, true)
}

func buildGraph(root string, useGoList bool) (*Graph, error) {
	workPath := filepath.Join(root, "go.work")
	workBytes, err := os.ReadFile(workPath)
	if err != nil {
		return nil, fmt.Errorf("read go.work: %w", err)
	}
	wf, err := modfile.ParseWork(workPath, workBytes, nil)
	if err != nil {
		return nil, fmt.Errorf("parse go.work: %w", err)
	}

	g := &Graph{
		WorkspaceModules: map[string]string{},
		ReverseDeps:      map[string]map[string]struct{}{},
	}

	type entry struct {
		path string
		dir  string
		mf   *modfile.File
	}
	var entries []entry
	for _, u := range wf.Use {
		modDir := filepath.Join(root, u.Path)
		gmPath := filepath.Join(modDir, "go.mod")
		gm, err := os.ReadFile(gmPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", gmPath, err)
		}
		mf, err := modfile.Parse(gmPath, gm, nil)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", gmPath, err)
		}
		g.WorkspaceModules[mf.Module.Mod.Path] = modDir
		entries = append(entries, entry{path: mf.Module.Mod.Path, dir: modDir, mf: mf})
	}

	for _, e := range entries {
		var deps []string
		if useGoList {
			deps, err = listDepsViaGoList(e.dir)
			if err != nil {
				return nil, fmt.Errorf("go list deps for %s: %w", e.path, err)
			}
		} else {
			deps = listDepsFromModFile(e.mf)
		}
		for _, dep := range deps {
			if dep == e.path {
				continue
			}
			if _, ok := g.WorkspaceModules[dep]; !ok {
				continue
			}
			if g.ReverseDeps[dep] == nil {
				g.ReverseDeps[dep] = map[string]struct{}{}
			}
			g.ReverseDeps[dep][e.path] = struct{}{}
		}
	}

	return g, nil
}

func listDepsFromModFile(mf *modfile.File) []string {
	out := make([]string, 0, len(mf.Require))
	for _, r := range mf.Require {
		out = append(out, r.Mod.Path)
	}
	return out
}

// listDepsViaGoList runs `go list -m -json all` in moduleDir and returns the
// module paths it requires (transitively, per MVS). Only usable when every
// require entry can actually be resolved by the module loader.
func listDepsViaGoList(moduleDir string) ([]string, error) {
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, stderr.String())
	}
	type mod struct {
		Path string
		Main bool
	}
	var deps []string
	dec := json.NewDecoder(&stdout)
	for dec.More() {
		var m mod
		if err := dec.Decode(&m); err != nil {
			return nil, err
		}
		if m.Main {
			continue
		}
		deps = append(deps, m.Path)
	}
	return deps, nil
}

// Affected returns the transitive closure of touched modules under ReverseDeps,
// including the touched modules themselves.
func (g *Graph) Affected(touched []string) []string {
	seen := map[string]struct{}{}
	var stack []string
	for _, t := range touched {
		if _, ok := g.WorkspaceModules[t]; !ok {
			continue
		}
		seen[t] = struct{}{}
		stack = append(stack, t)
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for consumer := range g.ReverseDeps[cur] {
			if _, ok := seen[consumer]; ok {
				continue
			}
			seen[consumer] = struct{}{}
			stack = append(stack, consumer)
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

func main() {
	args := os.Args[1:]
	useGoList := false
	if len(args) > 0 && args[0] == "-go-list" {
		useGoList = true
		args = args[1:]
	}
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: 01-revdep-graph [-go-list] <repo-root> <touched-module-path> [<touched-module-path>...]")
		os.Exit(2)
	}
	root := args[0]
	touched := args[1:]
	var (
		g   *Graph
		err error
	)
	if useGoList {
		g, err = BuildGraphWithGoList(root)
	} else {
		g, err = BuildGraph(root)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	affected := g.Affected(touched)
	fmt.Println(strings.Join(affected, "\n"))
}
