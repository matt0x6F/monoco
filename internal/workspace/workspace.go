// Package workspace loads a Go workspace (go.work) and exposes the
// workspace-internal module dependency graph, keyed by module path.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/mod/modfile"
)

// Module describes one module in the workspace.
type Module struct {
	Path   string // module path as declared in its go.mod (e.g. example.com/mono/api)
	Dir    string // absolute on-disk directory
	RelDir string // directory relative to workspace root (e.g. modules/api)
}

// Workspace is the loaded graph. Modules is keyed by module path;
// reverseDeps[A] is the set of workspace modules that require A.
type Workspace struct {
	Root    string
	Modules map[string]Module

	reverseDeps map[string]map[string]struct{}
}

// Load reads <root>/go.work, resolves each `use` entry's go.mod,
// and builds the workspace-internal reverse-dep graph by parsing
// each module's go.mod `require` lines.
//
// Only edges where BOTH endpoints are workspace modules are retained.
// External dependencies are ignored (they don't participate in propagation).
func Load(root string) (*Workspace, error) {
	workPath := filepath.Join(root, "go.work")
	workBytes, err := os.ReadFile(workPath)
	if err != nil {
		return nil, fmt.Errorf("read go.work: %w", err)
	}
	wf, err := modfile.ParseWork(workPath, workBytes, nil)
	if err != nil {
		return nil, fmt.Errorf("parse go.work: %w", err)
	}

	ws := &Workspace{
		Root:        root,
		Modules:     map[string]Module{},
		reverseDeps: map[string]map[string]struct{}{},
	}

	type parsed struct {
		mod  Module
		file *modfile.File
	}
	var parsedMods []parsed

	for _, u := range wf.Use {
		dir := filepath.Join(root, u.Path)
		gmBytes, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err != nil {
			return nil, fmt.Errorf("read %s/go.mod: %w", dir, err)
		}
		mf, err := modfile.Parse(filepath.Join(dir, "go.mod"), gmBytes, nil)
		if err != nil {
			return nil, fmt.Errorf("parse %s/go.mod: %w", dir, err)
		}
		mod := Module{Path: mf.Module.Mod.Path, Dir: dir, RelDir: u.Path}
		ws.Modules[mod.Path] = mod
		parsedMods = append(parsedMods, parsed{mod: mod, file: mf})
	}

	// Build reverse-dep edges using only workspace targets.
	for _, p := range parsedMods {
		for _, req := range p.file.Require {
			if _, ok := ws.Modules[req.Mod.Path]; !ok {
				continue
			}
			if ws.reverseDeps[req.Mod.Path] == nil {
				ws.reverseDeps[req.Mod.Path] = map[string]struct{}{}
			}
			ws.reverseDeps[req.Mod.Path][p.mod.Path] = struct{}{}
		}
	}

	return ws, nil
}

// Consumers returns the set of workspace modules that directly require mod.
// The slice is sorted for deterministic output.
func (w *Workspace) Consumers(mod string) []string {
	set, ok := w.reverseDeps[mod]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
