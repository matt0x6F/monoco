// Package workspace loads a Go workspace (go.work) and exposes the
// workspace-internal module dependency graph, keyed by module path.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/matt0x6f/monoco/internal/config"
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
//
// Load also reads the optional <root>/monoco.yaml manifest; any module
// listed under `exclude` is dropped from the returned workspace as if
// its go.work entry had never been declared. An absent manifest is not
// an error.
func Load(root string) (*Workspace, error) {
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	return loadWithConfig(root, cfg)
}

// LoadWithConfig is like Load but takes an already-parsed config. Useful
// in tests and for callers that need to inspect the manifest separately.
func LoadWithConfig(root string, cfg *config.Config) (*Workspace, error) {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return loadWithConfig(root, cfg)
}

func loadWithConfig(root string, cfg *config.Config) (*Workspace, error) {
	excluded := cfg.ExcludedSet()
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
		if _, skip := excluded[normalizeUsePath(u.Path)]; skip {
			continue
		}
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

// normalizeUsePath converts a go.work `use` entry (e.g. "./modules/foo"
// or "modules/foo") to the same slash-form the exclude list is normalized
// to (see config.Config.normalize).
func normalizeUsePath(p string) string {
	p = filepath.ToSlash(filepath.Clean(p))
	// filepath.Clean leaves leading "./" stripped on most platforms, but
	// be defensive for inputs like "./x" that Clean turns into "x" already.
	return p
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
