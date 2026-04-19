// Package affected computes the transitive reverse-dep closure of a set
// of touched modules over a workspace graph.
package affected

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/matt0x6f/monoco/internal/workspace"
)

// Compute returns the transitive closure of touched module paths under
// workspace reverse-dep edges, including the touched modules themselves.
// Modules not present in the workspace are silently ignored.
func Compute(ws *workspace.Workspace, touched []string) []string {
	seen := map[string]struct{}{}
	var stack []string
	for _, t := range touched {
		if _, ok := ws.Modules[t]; !ok {
			continue
		}
		if _, already := seen[t]; already {
			continue
		}
		seen[t] = struct{}{}
		stack = append(stack, t)
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, consumer := range ws.Consumers(cur) {
			if _, ok := seen[consumer]; ok {
				continue
			}
			seen[consumer] = struct{}{}
			stack = append(stack, consumer)
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// FromTouchedFiles maps a list of repo-relative file paths to the set of
// workspace module paths whose RelDir is a prefix of that file.
// Files outside any module are ignored. The returned list is sorted and unique.
func FromTouchedFiles(ws *workspace.Workspace, files []string) []string {
	set := map[string]struct{}{}
	for _, f := range files {
		for _, m := range ws.Modules {
			if isPathPrefix(m.RelDir, f) {
				set[m.Path] = struct{}{}
				break
			}
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func isPathPrefix(dir, file string) bool {
	dir = filepath.Clean(dir)
	file = filepath.Clean(file)
	if dir == "." || dir == "" {
		return true
	}
	return file == dir || strings.HasPrefix(file, dir+string(filepath.Separator))
}
