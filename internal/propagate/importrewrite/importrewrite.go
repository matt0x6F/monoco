// Package importrewrite rewrites Go source imports and go.mod module
// directives to add a major-version (/vN) suffix. It is used only when a
// plan includes a v1→v2+ boundary crossing (gated by --allow-major).
//
// Rewriting is AST-based (go/parser + go/ast + go/format). Files whose
// build constraints exclude them from the default platform are skipped
// and reported — the module-mode Verify step is the backstop for any
// import the rewriter missed.
package importrewrite

import (
	"bytes"
	"fmt"
	"go/build"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
)

// Rewrite describes one module-path substitution.
type Rewrite struct {
	OldPath string // e.g. "example.com/acme/foo"
	NewPath string // e.g. "example.com/acme/foo/v2"
}

// FileChange is one proposed edit to a .go source file.
type FileChange struct {
	Path string // absolute path
	Old  []byte
	New  []byte
}

// SkippedFile records a .go source file the rewriter intentionally did
// not touch, along with the reason. Verify will catch any missed imports
// at build time.
type SkippedFile struct {
	Path   string
	Reason string
}

// Report is the outcome of rewriting a consumer module.
type Report struct {
	Changes      []FileChange
	SkippedFiles []SkippedFile
}

// RewriteModuleDirective returns go.mod bytes with the `module` line set
// to newPath. It is a no-op (returns the input unchanged) if the module
// directive already equals newPath.
func RewriteModuleDirective(goModBytes []byte, newPath string) ([]byte, error) {
	mf, err := modfile.Parse("go.mod", goModBytes, nil)
	if err != nil {
		return nil, fmt.Errorf("parse go.mod: %w", err)
	}
	if mf.Module != nil && mf.Module.Mod.Path == newPath {
		return goModBytes, nil
	}
	if err := mf.AddModuleStmt(newPath); err != nil {
		return nil, fmt.Errorf("set module %s: %w", newPath, err)
	}
	mf.Cleanup()
	out, err := mf.Format()
	if err != nil {
		return nil, fmt.Errorf("format go.mod: %w", err)
	}
	return out, nil
}

// RewriteConsumer walks moduleDir, rewriting any .go file's import specs
// whose path matches a Rewrite.OldPath. Returns the set of changes plus
// any files skipped due to build-constraint mismatches.
//
// Skipped files are reported but not an error: Verify will catch any
// import the rewriter didn't update when building for the default
// platform. Exotic-platform files (e.g. foo_plan9.go) get the same
// treatment as before the rewrite — unchanged, and only reachable on
// that platform.
func RewriteConsumer(moduleDir string, rewrites []Rewrite) (*Report, error) {
	if len(rewrites) == 0 {
		return &Report{}, nil
	}
	index := map[string]string{}
	for _, r := range rewrites {
		if r.OldPath == "" || r.NewPath == "" {
			return nil, fmt.Errorf("invalid rewrite: old=%q new=%q", r.OldPath, r.NewPath)
		}
		index[r.OldPath] = r.NewPath
	}

	rep := &Report{}
	bctx := build.Default
	err := filepath.WalkDir(moduleDir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			name := d.Name()
			// Skip common non-source directories and nested modules.
			if path != moduleDir && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			if path != moduleDir {
				// Nested modules (their own go.mod) are out of scope:
				// the plan handles them as separate entries.
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return fs.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		dir := filepath.Dir(path)
		name := filepath.Base(path)
		match, err := bctx.MatchFile(dir, name)
		if err != nil {
			// Malformed build tag or similar — skip, record reason.
			rep.SkippedFiles = append(rep.SkippedFiles, SkippedFile{
				Path:   path,
				Reason: fmt.Sprintf("build.MatchFile: %v", err),
			})
			return nil
		}
		if !match {
			rep.SkippedFiles = append(rep.SkippedFiles, SkippedFile{
				Path:   path,
				Reason: "build constraints exclude this file on the default platform",
			})
			return nil
		}
		change, err := rewriteFile(path, index)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if change != nil {
			rep.Changes = append(rep.Changes, *change)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rep, nil
}

// rewriteFile parses path, rewrites matching import specs, and returns a
// FileChange if anything changed. Returns nil, nil when the file had no
// matching imports.
func rewriteFile(path string, index map[string]string) (*FileChange, error) {
	fset := token.NewFileSet()
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	modified := false
	for _, imp := range file.Imports {
		if imp.Path == nil {
			continue
		}
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		newPath, ok := index[p]
		if !ok {
			continue
		}
		imp.Path.Value = strconv.Quote(newPath)
		modified = true
	}
	if !modified {
		return nil, nil
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return nil, fmt.Errorf("format: %w", err)
	}
	return &FileChange{Path: path, Old: src, New: buf.Bytes()}, nil
}
