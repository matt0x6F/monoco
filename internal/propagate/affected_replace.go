package propagate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/matt0x6f/monoco/internal/workspace"
	"golang.org/x/mod/modfile"
)

// DirectFromReplaces returns the set of workspace modules that are the
// target of at least one workspace-local `replace` directive in any
// sibling module's go.mod. Those are the modules under active local
// development — the ones the engineer is about to release.
//
// A `replace` counts as "workspace-local" iff its New.Path is a
// relative path and resolves (from the requiring module's dir) to the
// on-disk directory of another workspace module.
//
// Returned slice is sorted by module path for determinism.
func DirectFromReplaces(ws *workspace.Workspace) ([]string, error) {
	dirByPath := map[string]string{}
	for _, m := range ws.Modules {
		abs, err := filepath.Abs(m.Dir)
		if err != nil {
			return nil, fmt.Errorf("abs %s: %w", m.Dir, err)
		}
		dirByPath[m.Path] = filepath.Clean(abs)
	}
	// Inverse map: abs dir → module path, for replace-target lookup.
	pathByDir := map[string]string{}
	for mp, dir := range dirByPath {
		pathByDir[dir] = mp
	}

	affected := map[string]struct{}{}
	for _, m := range ws.Modules {
		goModPath := filepath.Join(m.Dir, "go.mod")
		b, err := os.ReadFile(goModPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", goModPath, err)
		}
		mf, err := modfile.Parse(goModPath, b, nil)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", goModPath, err)
		}
		for _, r := range mf.Replace {
			// Only local-path replaces resolve to a directory.
			if r.New.Version != "" {
				continue
			}
			if !isLocalPath(r.New.Path) {
				continue
			}
			target := filepath.Clean(filepath.Join(m.Dir, r.New.Path))
			if mp, ok := pathByDir[target]; ok {
				affected[mp] = struct{}{}
			}
		}
	}

	out := make([]string, 0, len(affected))
	for p := range affected {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func isLocalPath(p string) bool {
	if p == "" {
		return false
	}
	// modfile treats `./x`, `../x`, `/abs` as local paths; any other value
	// is a module path replacement with a version.
	return p[0] == '.' || p[0] == '/' || (len(p) >= 2 && p[1] == ':') // windows drive
}
